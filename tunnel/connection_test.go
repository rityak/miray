package tunnel

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type partialReadPacketConn struct {
	C.PacketConn
	put    func()
	closed bool
}

type releaseCountingPacket struct {
	C.PacketAdapter
	drops atomic.Int32
}

func (p *releaseCountingPacket) Drop() { p.drops.Add(1) }

type closeBeforeEnqueueContext struct {
	context.Context
	closeSender func()
	probes      int
}

func (c *closeBeforeEnqueueContext) Done() <-chan struct{} {
	c.probes++
	if c.probes == 2 {
		// Cancel after Send's initial probe and before its enqueue select.
		c.closeSender()
	}
	return c.Context.Done()
}

func TestPacketSenderCloseDuringEnqueue(t *testing.T) {
	for i := 0; i < 64; i++ {
		sender := newPacketSender().(*packetSender)
		sender.ctx = &closeBeforeEnqueueContext{Context: sender.ctx, closeSender: sender.Close}
		packet := &releaseCountingPacket{}
		sender.Send(packet)
		if queued, drops := len(sender.ch), packet.drops.Load(); queued != 0 || drops != 1 {
			t.Fatalf("closed sender retains packet: queued=%d drops=%d", queued, drops)
		}
	}
}

func TestPacketSenderConcurrentClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		sender := newPacketSender().(*packetSender)
		packets := make([]releaseCountingPacket, 128)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for index := range packets {
			workers.Add(1)
			go func(packet *releaseCountingPacket) {
				defer workers.Done()
				<-start
				sender.Send(packet)
			}(&packets[index])
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			sender.Close()
		}()
		close(start)
		workers.Wait()
		if queued := len(sender.ch); queued != 0 {
			t.Fatalf("closed sender retains %d packets", queued)
		}
		for index := range packets {
			if drops := packets[index].drops.Load(); drops != 1 {
				t.Fatalf("packet %d dropped %d times", index, drops)
			}
		}
	}
}

func BenchmarkPacketSenderQueue(b *testing.B) {
	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	packet := &releaseCountingPacket{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sender.Send(packet)
		<-sender.ch
	}
}

func (c *partialReadPacketConn) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	return []byte("partial datagram"), c.put, nil, io.ErrUnexpectedEOF
}

func (c *partialReadPacketConn) SetReadDeadline(time.Time) error { return nil }

func (c *partialReadPacketConn) Close() error {
	c.closed = true
	return nil
}

func TestHandleUDPToLocalReleasesBufferOnReadError(t *testing.T) {
	for _, hasBuffer := range []bool{false, true} {
		puts := 0
		conn := &partialReadPacketConn{}
		if hasBuffer {
			conn.put = func() { puts++ }
		}
		sender := newPacketSender().(*packetSender)
		handleUDPToLocal(nil, &udpActivityConn{PacketConn: conn}, sender, t.Name(), netip.AddrPort{})
		wantPuts := 0
		if hasBuffer {
			wantPuts = 1
		}
		if puts != wantPuts || !conn.closed || sender.ctx.Err() == nil {
			t.Fatalf("hasBuffer=%t puts=%d want=%d connClosed=%t senderClosed=%t", hasBuffer, puts, wantPuts, conn.closed, sender.ctx.Err() != nil)
		}
	}
}

type addrRecordingPacketConn struct {
	C.PacketConn
	to net.Addr
}

func (pc *addrRecordingPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	pc.to = addr
	return len(b), nil
}

// A datagram of an established session to an IP goes to the mapped target
// without allocating (T-062).
func TestForwardMappedIPWithoutAllocs(t *testing.T) {
	s := newPacketSender().(*packetSender)
	defer s.Close()
	origin := &C.Metadata{NetWork: C.UDP, DstIP: netip.MustParseAddr("198.18.0.7"), DstPort: 443}
	s.AddMapping(origin, &C.Metadata{NetWork: C.UDP, DstIP: netip.MustParseAddr("192.0.2.9"), DstPort: 443})
	pc := &addrRecordingPacketConn{}
	data := make([]byte, 100)
	s.forward(pc, origin, data)
	if pc.to == nil || pc.to.String() != "192.0.2.9:443" {
		t.Fatalf("sent to %v, want the mapped target", pc.to)
	}
	if n := testing.AllocsPerRun(100, func() { s.forward(pc, origin, data) }); n != 0 {
		t.Fatalf("%v allocs per datagram, want 0", n)
	}
}
