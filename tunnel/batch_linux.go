//go:build linux

package tunnel

import (
	"errors"
	"fmt"
	"net"
	"time"
	"unsafe"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// batchTransparent lists the wrappers allowed between a session's tracker and a
// raw UDP socket. Any other layer may transform datagrams (encryption, framing)
// and must never be bypassed, so a batch writer exists only for DIRECT-like
// outbounds. Matching by type name keeps the other packages untouched (D-001)
// and fails safe: a renamed or new wrapper just disables batching.
var batchTransparent = map[string]bool{
	"*statistic.udpTracker":             true,
	"*callback.closeCallbackPacketConn": true,
	"*outbound.packetConn":              true,
	"*packet.enhanceUDPConn":            true,
}

// batchWriter sends several datagrams with one sendmmsg (S-005).
type batchWriter struct {
	v4    *ipv4.PacketConn
	v6    *ipv6.PacketConn
	msgs  []ipv4.Message
	count func(int64) // upload accounting of the bypassed tracker

	// UDP GSO (S-007): a run of same-destination, same-size datagrams goes out
	// as one buffer with UDP_SEGMENT and the kernel splits it.
	udp    *net.UDPConn
	noGSO  bool // set after the kernel refused a segmented send
	gsoBuf []byte
	gsoOOB []byte
}

const (
	maxGSOSegments = 64    // UDP_MAX_SEGMENTS on older kernels
	maxGSOBytes    = 65000 // below the 64 KiB UDP datagram limit
)

// trackActivity makes the sends of a batch writer extend the idle timeout, by
// the bytes that went out: a batch with one refused datagram still counts.
func trackActivity(w *batchWriter, ac *udpActivityConn) {
	upload := w.count
	w.count = func(n int64) {
		upload(n)
		if n > 0 {
			ac.lastWrite.Store(time.Now().UnixNano())
		}
	}
}

func newBatchWriter(pc C.PacketConn) *batchWriter {
	var count func(int64)
	var cur any = pc
	for {
		if udp, ok := cur.(*net.UDPConn); ok {
			if count == nil {
				return nil
			}
			w := &batchWriter{count: count, msgs: make([]ipv4.Message, maxBatch), udp: udp}
			for i := range w.msgs {
				w.msgs[i].Buffers = make([][]byte, 1)
			}
			if la, ok := udp.LocalAddr().(*net.UDPAddr); ok && la.IP.To4() != nil {
				w.v4 = ipv4.NewPacketConn(udp)
			} else {
				w.v6 = ipv6.NewPacketConn(udp) // dual-stack: Linux accepts AF_INET destinations
			}
			return w
		}
		if !batchTransparent[fmt.Sprintf("%T", cur)] {
			return nil
		}
		if c, ok := cur.(interface{ AddUpload(int64) }); ok {
			count = c.AddUpload
		}
		upstream, ok := cur.(N.WithUpstream)
		if !ok {
			return nil
		}
		cur = upstream.Upstream()
	}
}

// write sends datas in order: runs eligible for GSO as one segmented send each,
// everything else through sendmmsg.
func (w *batchWriter) write(datas [][]byte, addrs []*net.UDPAddr) error {
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	plain := 0 // datas[plain:i] wait for sendmmsg
	for i := 0; i < len(datas); {
		run := w.gsoRun(datas[i:], addrs[i:])
		if run < 2 {
			i++
			continue
		}
		keep(w.writeMulti(datas[plain:i], addrs[plain:i]))
		if err := w.writeGSO(datas[i:i+run], addrs[i]); isGSOError(err) {
			w.noGSO = true // unsupported here: send this run and everything later without GSO
			keep(w.writeMulti(datas[i:i+run], addrs[i:i+run]))
		} else {
			keep(err) // a transient error drops the run, as sendmmsg would
		}
		i += run
		plain = i
	}
	keep(w.writeMulti(datas[plain:], addrs[plain:]))
	return firstErr
}

// gsoRun returns how many leading datagrams can share one segmented send: same
// destination, equal size except a shorter last one.
func (w *batchWriter) gsoRun(datas [][]byte, addrs []*net.UDPAddr) int {
	if w.noGSO || len(datas) < 2 {
		return 0
	}
	size := len(datas[0])
	if size == 0 {
		return 0
	}
	n, total := 1, size
	for n < len(datas) && n < maxGSOSegments {
		next := len(datas[n])
		if addrs[n] != addrs[0] || next > size || next == 0 || total+next > maxGSOBytes {
			break
		}
		n++
		total += next
		if next < size {
			break // a shorter segment must be the last one
		}
	}
	return n
}

// isGSOError reports a refusal of UDP_SEGMENT itself: no kernel support
// (ENOPROTOOPT, EOPNOTSUPP), a segment above the path MTU (EINVAL) or a device
// without checksum offload (EIO, as handled by quic-go).
func isGSOError(err error) bool {
	return errors.Is(err, unix.EIO) || errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOPROTOOPT) || errors.Is(err, unix.EOPNOTSUPP)
}

func (w *batchWriter) writeGSO(datas [][]byte, addr *net.UDPAddr) error {
	buf := w.gsoBuf[:0]
	for _, d := range datas {
		buf = append(buf, d...)
	}
	w.gsoBuf = buf
	if w.gsoOOB == nil {
		w.gsoOOB = make([]byte, unix.CmsgSpace(2))
		h := (*unix.Cmsghdr)(unsafe.Pointer(&w.gsoOOB[0]))
		h.Level = unix.IPPROTO_UDP
		h.Type = unix.UDP_SEGMENT
		h.SetLen(unix.CmsgLen(2))
	}
	*(*uint16)(unsafe.Pointer(&w.gsoOOB[unix.CmsgLen(0)])) = uint16(len(datas[0]))
	n, _, err := w.udp.WriteMsgUDP(buf, w.gsoOOB, addr)
	if err != nil {
		return err
	}
	w.count(int64(n))
	return nil
}

func (w *batchWriter) writeMulti(datas [][]byte, addrs []*net.UDPAddr) error {
	if len(datas) == 0 {
		return nil
	}
	ms := w.msgs[:len(datas)]
	for i := range ms {
		ms[i].Buffers[0] = datas[i]
		ms[i].Addr = addrs[i]
		ms[i].N = 0
	}
	var firstErr error
	for len(ms) > 0 {
		var n int
		var err error
		if w.v4 != nil {
			n, err = w.v4.WriteBatch(ms, 0)
		} else {
			n, err = w.v6.WriteBatch(ms, 0)
		}
		if n < 0 { // x/net passes on the -1 of a failed sendmmsg
			n = 0
		}
		var sent int64
		for _, m := range ms[:n] {
			sent += int64(m.N)
		}
		w.count(sent)
		ms = ms[n:]
		if err == nil {
			if n == 0 {
				break
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		if errors.Is(err, net.ErrClosed) || len(ms) == 0 {
			break
		}
		// sendmmsg stops at the first datagram the kernel refuses (no route,
		// firewall): drop that one alone, as a sendto per datagram would
		ms = ms[1:]
	}
	return firstErr
}
