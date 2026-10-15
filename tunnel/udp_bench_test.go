package tunnel

// S-001: per-packet cost of the established UDP session path.

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/nat"
	C "github.com/metacubex/mihomo/constant"
)

type benchPacket struct {
	data []byte
	addr net.Addr
}

func (p *benchPacket) Data() []byte                                { return p.data }
func (p *benchPacket) WriteBack(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (p *benchPacket) Drop()                                       {}
func (p *benchPacket) LocalAddr() net.Addr                         { return p.addr }

// benchPC stands in for the outbound PacketConn. Only WriteTo and SetReadDeadline
// are reached on the established path; SetReadDeadline hits a real socket so its
// runtime timer cost is included.
type benchPC struct {
	C.PacketConn
	udp  *net.UDPConn
	done chan struct{}
}

func (pc *benchPC) WriteTo(b []byte, _ net.Addr) (int, error) {
	pc.done <- struct{}{}
	return len(b), nil
}

func (pc *benchPC) SetReadDeadline(t time.Time) error { return pc.udp.SetReadDeadline(t) }

func benchMetadata() *C.Metadata {
	return &C.Metadata{
		NetWork: C.UDP,
		Type:    C.TUN,
		SrcIP:   netip.MustParseAddr("198.18.0.1"),
		SrcPort: 40000,
		DstIP:   netip.MustParseAddr("1.1.1.1"),
		DstPort: 443,
	}
}

func BenchmarkUDPEstablished(b *testing.B) {
	OnRunning()
	udpInit.Do(initUDP)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	defer udp.Close()

	pc := &benchPC{udp: udp, done: make(chan struct{}, 1)}
	src := &net.UDPAddr{IP: net.IPv4(198, 18, 0, 1), Port: 40000}
	payload := make([]byte, 1200)
	first := &benchPacket{payload, src}

	sender := newPacketSender()
	natTable.GetOrCreate(src.String(), func() C.PacketSender { return sender })
	defer func() { sender.Close(); natTable.Delete(src.String()) }()
	sender.AddMapping(benchMetadata(), benchMetadata())
	go sender.Process(pc, nat.NewWriteBackProxy(first))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Tunnel.HandleUDPPacket(&benchPacket{payload, src}, benchMetadata())
		<-pc.done
	}
}

// Floor for comparison: one channel hop to a per-flow goroutine that writes.
func BenchmarkUDPBaselineHandoff(b *testing.B) {
	ch := make(chan []byte, 128)
	done := make(chan struct{}, 1)
	go func() {
		for p := range ch {
			_ = p
			done <- struct{}{}
		}
	}()
	defer close(ch)
	payload := make([]byte, 1200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- payload
		<-done
	}
}

func BenchmarkSetReadDeadline(b *testing.B) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	defer udp.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = udp.SetReadDeadline(time.Now().Add(udpTimeout))
	}
}

type benchFastPacket struct {
	data []byte
	dst  netip.AddrPort
}

func (p *benchFastPacket) Data() []byte                { return p.data }
func (p *benchFastPacket) Drop()                       {}
func (p *benchFastPacket) Destination() netip.AddrPort { return p.dst }

// S-004/T-008: the same established session through the fast path. The packet
// object is reused (its Drop is a no-op), so allocs/op are the tunnel's own.
func BenchmarkUDPFastEstablished(b *testing.B) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	defer udp.Close()

	pc := &udpActivityConn{PacketConn: &benchPC{udp: udp, done: make(chan struct{}, 1)}}
	done := pc.PacketConn.(*benchPC).done
	src := netip.MustParseAddrPort("198.18.0.1:40000")
	packet := &benchFastPacket{make([]byte, 1200), netip.MustParseAddrPort("1.1.1.1:443")}

	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	sender.AddMapping(benchMetadata(), benchMetadata())
	registerFlow(b, src, sender)
	go sender.Process(pc, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Tunnel.HandleUDPPacketFast(src, packet)
		<-done
	}
}
