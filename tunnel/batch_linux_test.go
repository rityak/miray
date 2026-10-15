//go:build linux

package tunnel

import (
	"net"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/packet"
	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/net/ipv6"
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
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
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
