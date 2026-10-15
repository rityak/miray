//go:build linux

package tunnel

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/packet"
	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// sendmmsg delivers every datagram in order, with its exact payload, and the
// bytes are accounted once (S-005).
func TestBatchWriterDelivers(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	conn, err := net.ListenUDP("udp", nil) // dual-stack, like DIRECT
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var counted int64
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	dst := sink.LocalAddr().(*net.UDPAddr)
	var datas [][]byte
	var addrs []*net.UDPAddr
	for i := 0; i < maxBatch; i++ {
		datas = append(datas, []byte{byte(i), byte(i), byte(i)})
		addrs = append(addrs, dst)
	}
	if err := w.write(datas, addrs); err != nil {
		t.Fatal(err)
	}
	if counted != int64(3*maxBatch) {
		t.Fatalf("counted %d bytes, want %d", counted, 3*maxBatch)
	}
	buf := make([]byte, 16)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < maxBatch; i++ {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != 3 || buf[0] != byte(i) {
			t.Fatalf("datagram %d: %v %v", i, buf[:n], err)
		}
	}
}

// A chain with an unknown layer (any protocol wrapper) must not get a batch
// writer: writing to the socket below it would bypass that layer.
func TestBatchWriterRefusesUnknownLayer(t *testing.T) {
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	var pc C.PacketConn = &lifecyclePC{epc: packet.NewEnhancePacketConn(udp)}
	if w := newBatchWriter(pc); w != nil {
		t.Fatal("batch writer created through an unknown layer")
	}
}

// A same-destination run goes out as one UDP_SEGMENT send and still arrives as
// separate datagrams, in order, including a shorter last one (S-007).
func TestBatchWriterGSO(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var counted int64
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	dst, dst2 := sink.LocalAddr().(*net.UDPAddr), other.LocalAddr().(*net.UDPAddr)
	sizes := []int{1200, 1200, 1200, 800, 1200, 1200, 500}
	var datas [][]byte
	var addrs []*net.UDPAddr
	var want int64
	for i, size := range sizes {
		d := make([]byte, size)
		d[0] = byte(i)
		datas = append(datas, d)
		addrs = append(addrs, dst)
		want += int64(size)
	}
	datas = append(datas, []byte{99}) // another destination ends the run
	addrs = append(addrs, dst2)
	want++
	if got := w.gsoRun(datas, addrs); got != 4 {
		t.Fatalf("first run = %d, want 4 (equal sizes, shorter last)", got)
	}
	if err := w.write(datas, addrs); err != nil {
		t.Fatal(err)
	}
	if w.noGSO {
		t.Skip("kernel refused UDP_SEGMENT")
	}
	if counted != want {
		t.Fatalf("counted %d bytes, want %d", counted, want)
	}
	buf := make([]byte, 2048)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i, size := range sizes {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != size || buf[0] != byte(i) {
			t.Fatalf("datagram %d: len %d first %d err %v, want len %d", i, n, buf[0], err, size)
		}
	}
	_ = other.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _, err := other.ReadFrom(buf); err != nil || n != 1 || buf[0] != 99 {
		t.Fatalf("other destination: %v %v", buf[:n], err)
	}
}

func TestIsGSOError(t *testing.T) {
	for _, err := range []error{unix.EIO, unix.EINVAL, unix.ENOPROTOOPT, unix.EOPNOTSUPP,
		&net.OpError{Op: "write", Err: os.NewSyscallError("sendmsg", unix.EIO)}} {
		if !isGSOError(err) {
			t.Errorf("%v: want GSO refusal", err)
		}
	}
	for _, err := range []error{nil, unix.ENOBUFS, unix.ECONNREFUSED, net.ErrClosed} {
		if isGSOError(err) {
			t.Errorf("%v: transient error disables GSO", err)
		}
	}
}
