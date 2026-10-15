//go:build linux

package tunnel

import (
	"fmt"
	"net"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
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
}

func newBatchWriter(pc C.PacketConn) *batchWriter {
	var count func(int64)
	var cur any = pc
	for {
		if udp, ok := cur.(*net.UDPConn); ok {
			if count == nil {
				return nil
			}
			w := &batchWriter{count: count, msgs: make([]ipv4.Message, maxBatch)}
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

func (w *batchWriter) write(datas [][]byte, addrs []*net.UDPAddr) error {
	ms := w.msgs[:len(datas)]
	for i := range ms {
		ms[i].Buffers[0] = datas[i]
		ms[i].Addr = addrs[i]
		ms[i].N = 0
	}
	for len(ms) > 0 {
		var n int
		var err error
		if w.v4 != nil {
			n, err = w.v4.WriteBatch(ms, 0)
		} else {
			n, err = w.v6.WriteBatch(ms, 0)
		}
		var sent int64
		for _, m := range ms[:n] {
			sent += int64(m.N)
		}
		w.count(sent)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		ms = ms[n:]
	}
	return nil
}
