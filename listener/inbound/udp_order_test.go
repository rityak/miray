package inbound_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type packetListener interface {
	ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error)
}

// testUDPEchoOrder sends a burst of datagrams of one flow through out and
// checks the echoes come back in the order sent: a QUIC receive loop must not
// hand each datagram to its own goroutine (T-063, T-064). Losses are allowed.
func testUDPEchoOrder(t *testing.T, tunnel *TestTunnel, out packetListener) {
	tunnel.HandleUDPPacketFn = func(packet C.UDPPacket, metadata *C.Metadata) {
		_, _ = packet.WriteBack(packet.Data(), metadata.UDPAddr())
		packet.Drop()
	}
	dst := netip.MustParseAddrPort("192.0.2.1:53")
	pc, err := out.ListenPacketContext(context.Background(), &C.Metadata{NetWork: C.UDP, DstIP: dst.Addr(), DstPort: dst.Port()})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	const count = 200
	for i := 0; i < count; i++ {
		if _, err := pc.WriteTo([]byte{byte(i), 0, 0, 0}, net.UDPAddrFromAddrPort(dst)); err != nil {
			t.Fatal(err)
		}
	}
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	last, got, reordered := -1, 0, 0
	for got < count {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		if n != 4 {
			t.Fatalf("echo of %d bytes", n)
		}
		if int(buf[0]) < last {
			reordered++
		}
		last = int(buf[0])
		got++
	}
	if got < count/5 { // a burst over QUIC on a busy 2-CPU host loses up to ~60%; order is what is checked

		t.Fatalf("only %d of %d echoes", got, count)
	}
	if reordered > 0 {
		t.Fatalf("%d of %d echoes came back out of order", reordered, got)
	}
}
