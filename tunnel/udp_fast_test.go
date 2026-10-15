package tunnel

import (
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type recordPC struct {
	C.PacketConn
	writes chan netip.AddrPort
}

func (pc *recordPC) WriteTo(b []byte, addr net.Addr) (int, error) {
	pc.writes <- addr.(*net.UDPAddr).AddrPort()
	return len(b), nil
}

type dropCounter struct {
	benchFastPacket
	drops *atomic.Int32
}

func (p *dropCounter) Drop() { p.drops.Add(1) }

// Fast path (S-004): a mapped destination is rewritten like the regular path,
// an unmapped one takes the regular resolve path once and is mapped afterwards,
// every packet is released exactly once, and an unknown flow is refused.
func TestUDPFastPath(t *testing.T) {
	src := netip.MustParseAddrPort("198.18.0.1:40001")
	fake := netip.MustParseAddr("198.18.5.5")
	real := netip.MustParseAddr("1.1.1.1")
	other := netip.MustParseAddrPort("8.8.8.8:53")

	if Tunnel.HandleUDPPacketFast(src, &benchFastPacket{nil, other}) {
		t.Fatal("accepted a packet without a session")
	}

	pc := &recordPC{writes: make(chan netip.AddrPort, 4)}
	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	origin := &C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: fake, DstPort: 443}
	target := &C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: real, DstPort: 443}
	sender.AddMapping(origin, target)
	udpFast.Store(src, sender)
	defer udpFast.Delete(src)
	go sender.Process(pc, nil)

	var drops atomic.Int32
	send := func(dst netip.AddrPort) netip.AddrPort {
		t.Helper()
		if !Tunnel.HandleUDPPacketFast(src, &dropCounter{benchFastPacket{[]byte{1}, dst}, &drops}) {
			t.Fatal("refused a packet of an established session")
		}
		select {
		case got := <-pc.writes:
			return got
		case <-time.After(time.Second):
			t.Fatal("packet not written")
			return netip.AddrPort{}
		}
	}

	if got := send(netip.AddrPortFrom(fake, 443)); got != netip.AddrPortFrom(real, 443) {
		t.Fatalf("mapped destination written to %s", got)
	}
	if got := send(other); got != other {
		t.Fatalf("new destination written to %s", got)
	}
	sender.mappingMutex.RLock()
	mapped := sender.fastTarget[other.Addr()]
	sender.mappingMutex.RUnlock()
	if mapped != other.Addr() {
		t.Fatalf("new destination not mapped for the fast path: %s", mapped)
	}
	for i := 0; drops.Load() < 2 && i < 100; i++ { // Drop runs after WriteTo returns
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if n := drops.Load(); n != 2 {
		t.Fatalf("drops = %d, want 2", n)
	}
}
