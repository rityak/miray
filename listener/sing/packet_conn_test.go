package sing

import (
	"context"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/inbound"
	C "github.com/metacubex/mihomo/constant"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
)

// scriptConn hands out one datagram per destination of dsts, then reports closed.
type scriptConn struct {
	dsts  []M.Socksaddr
	local net.Addr
}

func (c *scriptConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if len(c.dsts) == 0 {
		return M.Socksaddr{}, net.ErrClosed
	}
	dst := c.dsts[0]
	c.dsts = c.dsts[1:]
	buffer.Extend(100)
	return dst, nil
}
func (c *scriptConn) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }
func (c *scriptConn) Close() error                                   { return nil }
func (c *scriptConn) LocalAddr() net.Addr                            { return c.local }
func (c *scriptConn) SetDeadline(time.Time) error                    { return nil }
func (c *scriptConn) SetReadDeadline(time.Time) error                { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error               { return nil }

type recordTunnel struct {
	nopTunnel
	got []*C.Metadata
}

func (r *recordTunnel) HandleUDPPacket(p C.UDPPacket, m *C.Metadata) {
	r.got = append(r.got, m)
	p.Drop()
}

// The metadata of every datagram of a packet connection is what the per-datagram
// build gave (WithDstAddr, WithSrcAddr, WithInAddr, then the listener's and the
// context's additions), while the destination changes from datagram to datagram (T-128).
func TestNewPacketConnectionMetadata(t *testing.T) {
	rt := &recordTunnel{}
	h, err := NewListenerHandler(ListenerConfig{Tunnel: rt, Type: C.SHADOWSOCKS,
		Additions: []inbound.Addition{inbound.WithInName("ss-in"), inbound.WithSpecialRules("r")}})
	if err != nil {
		t.Fatal(err)
	}
	src := M.SocksaddrFromNetIP(netip.MustParseAddrPort("203.0.113.7:40000"))
	local := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 8388}
	dsts := []M.Socksaddr{
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443")),
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443")),
		M.ParseSocksaddrHostPort("example.com", 53),
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("[2001:db8::1]:8443")),
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("[::ffff:8.8.8.8]:53")),
	}
	ctx := WithAdditions(context.Background(), inbound.WithInUser("u"))
	_ = h.NewPacketConnection(ctx, &scriptConn{dsts: append([]M.Socksaddr(nil), dsts...), local: local}, M.Metadata{Source: src})
	if len(rt.got) != len(dsts) {
		t.Fatalf("%d datagrams reached the tunnel, want %d", len(rt.got), len(dsts))
	}
	for i, dst := range dsts {
		want := &C.Metadata{NetWork: C.UDP, Type: C.SHADOWSOCKS, RawSrcAddr: rawUDPAddr(src), RawDstAddr: rawUDPAddr(dst)}
		inbound.ApplyAdditions(want, inbound.WithDstAddr(dst), inbound.WithSrcAddr(src), inbound.WithInAddr(local))
		inbound.ApplyAdditions(want, h.Additions...)
		inbound.ApplyAdditions(want, getAdditions(ctx)...)
		if !reflect.DeepEqual(rt.got[i], want) {
			t.Errorf("datagram %d to %v:\n got %+v\nwant %+v", i, dst, *rt.got[i], *want)
		}
	}
	for i := range rt.got {
		for j := i + 1; j < len(rt.got); j++ {
			if rt.got[i] == rt.got[j] {
				t.Fatalf("datagrams %d and %d share one Metadata", i, j)
			}
		}
	}
}

// Allocations per datagram of an established packet connection (T-128).
func BenchmarkNewPacketConnection(b *testing.B) {
	h, err := NewListenerHandler(ListenerConfig{Tunnel: nopTunnel{}, Type: C.SHADOWSOCKS})
	if err != nil {
		b.Fatal(err)
	}
	dst := M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443"))
	dsts := make([]M.Socksaddr, b.N)
	for i := range dsts {
		dsts[i] = dst
	}
	conn := &scriptConn{dsts: dsts, local: &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 8388}}
	src := M.SocksaddrFromNetIP(netip.MustParseAddrPort("203.0.113.7:40000"))
	b.ReportAllocs()
	b.ResetTimer()
	_ = h.NewPacketConnection(context.Background(), conn, M.Metadata{Source: src})
}

// fakeFlow takes datagrams once open; it records what it took.
type fakeFlow struct {
	open bool
	took []netip.AddrPort
}

func (f *fakeFlow) Send(p C.UDPFastPacket) bool {
	if !f.open {
		return false
	}
	f.took = append(f.took, p.Destination())
	p.Drop()
	return true
}

type flowTunnel struct {
	recordTunnel
	flow  *fakeFlow
	flows []C.UDPFlow // of the datagrams HandleUDPPacket got
}

func (t *flowTunnel) NewUDPFlow() C.UDPFlow { return t.flow }

func (t *flowTunnel) HandleUDPPacket(p C.UDPPacket, m *C.Metadata) {
	t.flows = append(t.flows, p.(C.UDPFlowPacket).UDPFlow())
	t.recordTunnel.HandleUDPPacket(p, m)
}

// Datagrams of a packet connection to an IP go to its flow once the flow takes
// them, with the destination unmapped (T-133); a domain, port 0 and every datagram
// the flow refuses take the regular path and carry the flow to the session.
func TestNewPacketConnectionFlow(t *testing.T) {
	src := M.SocksaddrFromNetIP(netip.MustParseAddrPort("203.0.113.7:40000"))
	local := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 8388}
	dsts := []M.Socksaddr{
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443")),
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("[::ffff:8.8.8.8]:53")),
		M.ParseSocksaddrHostPort("example.com", 53),
		M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:0")),
	}
	run := func(open bool) *flowTunnel {
		ft := &flowTunnel{flow: &fakeFlow{open: open}}
		h, err := NewListenerHandler(ListenerConfig{Tunnel: ft, Type: C.SHADOWSOCKS})
		if err != nil {
			t.Fatal(err)
		}
		_ = h.NewPacketConnection(context.Background(), &scriptConn{dsts: append([]M.Socksaddr(nil), dsts...), local: local}, M.Metadata{Source: src})
		return ft
	}

	closed := run(false)
	if len(closed.got) != len(dsts) {
		t.Fatalf("flow without a session: %d datagrams on the regular path, want %d", len(closed.got), len(dsts))
	}
	for i, f := range closed.flows {
		if f != C.UDPFlow(closed.flow) {
			t.Fatalf("regular datagram %d carries flow %v", i, f)
		}
	}

	open := run(true)
	wantFast := []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:443"), netip.MustParseAddrPort("8.8.8.8:53")}
	if !reflect.DeepEqual(open.flow.took, wantFast) {
		t.Fatalf("flow took %v, want %v", open.flow.took, wantFast)
	}
	if len(open.got) != 2 || open.got[0].Host != "example.com" || open.got[1].DstPort != 0 {
		t.Fatalf("regular path got %d datagrams: %+v", len(open.got), open.got)
	}
}

// Allocations per datagram of an established packet connection on its flow (T-133).
func BenchmarkNewPacketConnectionFlow(b *testing.B) {
	h, err := NewListenerHandler(ListenerConfig{Tunnel: &flowTunnel{flow: &fakeFlow{open: true}}, Type: C.SHADOWSOCKS})
	if err != nil {
		b.Fatal(err)
	}
	dst := M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443"))
	dsts := make([]M.Socksaddr, b.N)
	for i := range dsts {
		dsts[i] = dst
	}
	conn := &scriptConn{dsts: dsts, local: &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 8388}}
	src := M.SocksaddrFromNetIP(netip.MustParseAddrPort("203.0.113.7:40000"))
	b.ReportAllocs()
	b.ResetTimer()
	_ = h.NewPacketConnection(context.Background(), conn, M.Metadata{Source: src})
}
