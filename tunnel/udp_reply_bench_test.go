package tunnel_test

// T-100: per-datagram cost of the reply path of an established UDP session, through the
// wrappers a proxied TUN session really has, against a bare read → write loop (what
// sing-box's bufio.CopyPacket does). The difference is the cost of our layers, which a
// profile from the stand cannot resolve (P-028).

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/pool"
	"github.com/metacubex/mihomo/component/nat"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/sing"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
)

const replySize = 1200

// replySrc is the outbound conn of a proxy (ss, hy2...): n datagrams from one server, then
// closed. It is not a syscall.Conn, so outbound.NewPacketConn puts the deadline wrapper on
// it, as on a real proxy conn.
type replySrc struct {
	net.PacketConn
	n    int
	from *net.UDPAddr
}

func (s *replySrc) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	if s.n == 0 {
		return nil, nil, nil, net.ErrClosed
	}
	s.n--
	b := pool.Get(pool.UDPBufferSize)
	return b[:replySize], func() { _ = pool.Put(b) }, s.from, nil
}

func (s *replySrc) ReadFrom(p []byte) (int, net.Addr, error) {
	data, put, addr, err := s.WaitReadFrom()
	if err != nil {
		return 0, nil, err
	}
	n := copy(p, data)
	put()
	return n, addr, nil
}

func (s *replySrc) Close() error                     { return nil }
func (s *replySrc) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (s *replySrc) SetReadDeadline(time.Time) error  { return nil }
func (s *replySrc) SetDeadline(time.Time) error      { return nil }
func (s *replySrc) SetWriteDeadline(time.Time) error { return nil }

// tunWriter stands in for the sing-tun UDP writer: it takes the buffer and drops it.
type tunWriter struct{}

func (tunWriter) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }

type captureTunnel struct{ p C.UDPPacket }

func (*captureTunnel) HandleTCPConn(net.Conn, *C.Metadata)            {}
func (t *captureTunnel) HandleUDPPacket(p C.UDPPacket, _ *C.Metadata) { t.p = p }
func (*captureTunnel) NatTable() C.NatTable                           { return nil }

// tunPacket returns the first datagram of a TUN flow as listener/sing hands it to tunnel:
// its WriteBack is the real reply chain down to the TUN writer.
func tunPacket(b *testing.B) C.UDPPacket {
	capture := &captureTunnel{}
	h, err := sing.NewListenerHandler(sing.ListenerConfig{Tunnel: capture, Type: C.TUN})
	if err != nil {
		b.Fatal(err)
	}
	src := netip.MustParseAddrPort("198.18.0.1:40000")
	md := M.Metadata{Source: M.SocksaddrFromNetIP(src), Destination: M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443"))}
	first := buf.NewSize(replySize)
	first.Extend(replySize)
	h.NewPacket(context.Background(), src, first, md, func(network.PacketConn) network.PacketWriter { return tunWriter{} })
	if capture.p == nil {
		b.Fatal("listener did not hand the packet to tunnel")
	}
	return capture.p
}

func BenchmarkUDPReplyTUN(b *testing.B) {
	from := &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 443}
	metadata := &C.Metadata{NetWork: C.UDP, Type: C.TUN, SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 40000,
		DstIP: netip.MustParseAddr("1.1.1.1"), DstPort: 443}
	adapter := outbound.NewAutoCloseProxyAdapter(outbound.NewDirect())
	rawPc := outbound.NewPacketConn(&replySrc{n: b.N, from: from}, outbound.NewDirect())
	if ref, ok := rawPc.(outbound.AddRef); ok {
		ref.AddRef(adapter) // as autoCloseProxyAdapter.ListenPacketContext does
	}
	pc := statistic.NewUDPTracker(rawPc, statistic.DefaultManager, metadata, nil, 0, 0, true)
	packet := tunPacket(b)
	defer packet.Drop()
	b.ReportAllocs()
	b.SetBytes(replySize)
	b.ResetTimer()
	tunnel.RelayReplies(nat.NewWriteBackProxy(packet), pc, b.Name(), from.AddrPort())
}

// Floor: the same datagrams read and written with nothing between, one buffer per datagram.
func BenchmarkUDPReplyFloor(b *testing.B) {
	src := &replySrc{n: b.N, from: &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 443}}
	dst := M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443"))
	var w network.PacketWriter = tunWriter{}
	b.ReportAllocs()
	b.SetBytes(replySize)
	b.ResetTimer()
	for {
		data, put, _, err := src.WaitReadFrom()
		if err != nil {
			return
		}
		buffer := buf.NewSize(len(data))
		_, _ = buffer.Write(data)
		put()
		_ = w.WritePacket(buffer, dst)
	}
}
