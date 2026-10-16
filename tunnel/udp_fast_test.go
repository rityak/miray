package tunnel

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/packet"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/sniffer"
	C "github.com/metacubex/mihomo/constant"
)

func registerFlow(tb testing.TB, src netip.AddrPort, ps *packetSender) *flowState {
	flow := &flowState{src: src}
	flow.sender.Store(ps)
	udpFlows.Store(src, flow)
	tb.Cleanup(func() { udpFlows.Delete(src) })
	return flow
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal(what)
}

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

// A mapped destination is rewritten like the regular path and its address is
// cached, an unmapped one takes the regular resolve path once, every packet is
// released exactly once, and an unknown flow is refused.
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
	sender.AddMapping(
		&C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: fake, DstPort: 443},
		&C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: real, DstPort: 443},
	)
	registerFlow(t, src, sender)
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

	for i := 0; i < 2; i++ {
		if got := send(netip.AddrPortFrom(fake, 443)); got != netip.AddrPortFrom(real, 443) {
			t.Fatalf("mapped destination written to %s", got)
		}
	}
	for i := 0; i < 2; i++ { // first via the regular resolve path, then cached
		if got := send(other); got != other {
			t.Fatalf("new destination written to %s", got)
		}
	}
	sender.mappingMutex.RLock()
	cached := len(sender.fastAddr)
	sender.mappingMutex.RUnlock()
	if cached != 2 {
		t.Fatalf("cached addresses = %d, want 2", cached)
	}
	eventually(t, "packets not released exactly once", func() bool { return drops.Load() == 4 })
}

// While regular-path datagrams of the flow are still queued for the worker, the
// fast path must refuse, so it never overtakes them.
func TestUDPFastPathOrder(t *testing.T) {
	src := netip.MustParseAddrPort("198.18.0.1:40002")
	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	flow := registerFlow(t, src, sender)
	p := &benchFastPacket{nil, netip.MustParseAddrPort("1.1.1.1:443")}

	flow.queued.Add(1)
	if Tunnel.HandleUDPPacketFast(src, p) {
		t.Fatal("fast path taken while a regular datagram is queued")
	}
	flow.queued.Add(-1)
	if !Tunnel.HandleUDPPacketFast(src, p) {
		t.Fatal("fast path refused after the queue drained")
	}
}

// Sessions with a QUIC sniffer wrapper still get the fast path.
func TestUnwrapSniffedSender(t *testing.T) {
	quic, err := sniffer.NewQUICSniffer(sniffer.SnifferConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ps := newPacketSender().(*packetSender)
	wrapped := quic.WrapperSender(ps, nil)
	if got, ok := unwrapPacketSender(wrapped); !ok || got != ps {
		t.Fatalf("unwrap = %v, %v", got, ok)
	}
}

// lifecyclePC is a DIRECT-like outbound over a real loopback socket.
type lifecyclePC struct {
	C.PacketConn
	epc packet.EnhancePacketConn
}

func (c *lifecyclePC) WaitReadFrom() ([]byte, func(), net.Addr, error) { return c.epc.WaitReadFrom() }
func (c *lifecyclePC) WriteTo(b []byte, a net.Addr) (int, error)       { return c.epc.WriteTo(b, a) }
func (c *lifecyclePC) SetReadDeadline(t time.Time) error               { return c.epc.SetReadDeadline(t) }
func (c *lifecyclePC) Close() error                                    { return c.epc.Close() }
func (c *lifecyclePC) Chains() C.Chain                                 { return C.Chain{"DIRECT"} }
func (c *lifecyclePC) ProviderChains() C.Chain                         { return nil }
func (c *lifecyclePC) RemoteDestination() string                       { return "" }

type lifecycleProxy struct {
	C.Proxy
	fail atomic.Bool
}

func (p *lifecycleProxy) Name() string { return "DIRECT" }

func (p *lifecycleProxy) ListenPacketContext(context.Context, *C.Metadata) (C.PacketConn, error) {
	if p.fail.Load() {
		return nil, resolver.ErrIPNotFound // stops retry at once
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	return &lifecyclePC{epc: packet.NewEnhancePacketConn(udp)}, nil
}

// Full regular path: the first datagram creates the session and registers the
// flow, later ones take the fast path, and the flow is forgotten when the
// session expires or its dial fails.
func TestUDPFlowLifecycle(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	proxy := &lifecycleProxy{}
	oldProxies, oldMode, oldTimeout := proxies, mode.Load(), udpTimeout
	proxies, udpTimeout = map[string]C.Proxy{"DIRECT": proxy}, 300*time.Millisecond
	mode.Store(Direct)
	defer func() { proxies, udpTimeout = oldProxies, oldTimeout; mode.Store(oldMode) }()
	OnRunning()

	dst := sink.LocalAddr().(*net.UDPAddr).AddrPort()
	ingress := func(src netip.AddrPort, payload byte) {
		Tunnel.HandleUDPPacket(&benchPacket{[]byte{payload}, net.UDPAddrFromAddrPort(src)}, &C.Metadata{
			NetWork: C.UDP, Type: C.TUN,
			SrcIP: src.Addr(), SrcPort: src.Port(), DstIP: dst.Addr(), DstPort: dst.Port(),
		})
	}
	recv := func(want byte) {
		t.Helper()
		b := make([]byte, 16)
		_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := sink.ReadFrom(b)
		if err != nil || n != 1 || b[0] != want {
			t.Fatalf("sink got %v %v, want payload %d", b[:n], err, want)
		}
	}
	registered := func(src netip.AddrPort) bool {
		flow, ok := udpFlows.Load(src)
		return ok && flow.sender.Load() != nil && flow.queued.Load() == 0
	}
	gone := func(src netip.AddrPort) func() bool {
		return func() bool {
			_, ok := udpFlows.Load(src)
			_, session := natTable.Get(net.UDPAddrFromAddrPort(src).String())
			return !ok && !session
		}
	}

	src := netip.MustParseAddrPort("198.18.0.1:40003")
	ingress(src, 1)
	recv(1)
	eventually(t, "flow not registered", func() bool { return registered(src) })

	if !Tunnel.HandleUDPPacketFast(src, &benchFastPacket{[]byte{2}, dst}) {
		t.Fatal("fast path refused for an established session")
	}
	recv(2)

	eventually(t, "flow not forgotten after the session expired", gone(src))
	if Tunnel.HandleUDPPacketFast(src, &benchFastPacket{[]byte{3}, dst}) {
		t.Fatal("fast path taken after the session expired")
	}

	proxy.fail.Store(true)
	failed := netip.MustParseAddrPort("198.18.0.1:40004")
	ingress(failed, 4)
	eventually(t, "flow not forgotten after a failed dial", gone(failed))
}

// fakeIPOnly is a fake-ip mapper that knows the pool but holds no records.
type fakeIPOnly struct{ pool netip.Prefix }

func (m fakeIPOnly) FakeIPEnabled() bool                    { return true }
func (m fakeIPOnly) MappingEnabled() bool                   { return true }
func (m fakeIPOnly) IsFakeIP(ip netip.Addr) bool            { return m.pool.Contains(ip) }
func (m fakeIPOnly) IsFakeBroadcastIP(netip.Addr) bool      { return false }
func (m fakeIPOnly) IsExistFakeIP(netip.Addr) bool          { return false }
func (m fakeIPOnly) FindHostByIP(netip.Addr) (string, bool) { return "", false }
func (m fakeIPOnly) FlushFakeIP() error                     { return nil }
func (m fakeIPOnly) InsertHostByIP(netip.Addr, string)      {}
func (m fakeIPOnly) StoreFakePoolState()                    {}

// A datagram of an established session to a fake IP whose record is gone is
// dropped, as the regular path drops it, instead of being sent to the fake IP
// itself (T-057).
func TestUDPFastPathDropsMissingFakeIP(t *testing.T) {
	previous := resolver.DefaultHostMapper
	resolver.DefaultHostMapper = fakeIPOnly{netip.MustParsePrefix("198.18.0.0/16")}
	t.Cleanup(func() { resolver.DefaultHostMapper = previous })

	src := netip.MustParseAddrPort("198.18.0.1:40011")
	pc := &recordPC{writes: make(chan netip.AddrPort, 4)}
	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	registerFlow(t, src, sender)
	go sender.Process(pc, nil)

	var drops atomic.Int32
	stale := netip.MustParseAddrPort("198.18.9.9:443")
	real := netip.MustParseAddrPort("192.0.2.1:443")
	for _, dst := range []netip.AddrPort{stale, real} {
		if !Tunnel.HandleUDPPacketFast(src, &dropCounter{benchFastPacket{[]byte{1}, dst}, &drops}) {
			t.Fatal("refused a packet of an established session")
		}
	}
	select {
	case got := <-pc.writes:
		if got != real {
			t.Fatalf("written to %s, want only %s", got, real)
		}
	case <-time.After(time.Second):
		t.Fatal("real destination not written")
	}
	eventually(t, "packets not released", func() bool { return drops.Load() == 2 })
	select {
	case got := <-pc.writes:
		t.Fatalf("unexpected write to %s", got)
	default:
	}
}
