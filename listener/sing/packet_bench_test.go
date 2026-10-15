package sing

// S-001: per-packet ingress cost of ListenerHandler.NewPacket (TUN/udpnat path).

import (
	"context"
	"net"
	"net/netip"
	"testing"

	C "github.com/metacubex/mihomo/constant"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
)

type nopTunnel struct{}

func (nopTunnel) HandleTCPConn(net.Conn, *C.Metadata)          {}
func (nopTunnel) HandleUDPPacket(p C.UDPPacket, _ *C.Metadata) { p.Drop() }
func (nopTunnel) NatTable() C.NatTable                         { return nil }

type nopWriter struct{}

func (nopWriter) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }

func benchNewPacket(b *testing.B, typ C.Type) {
	h, err := NewListenerHandler(ListenerConfig{Tunnel: nopTunnel{}, Type: typ})
	if err != nil {
		b.Fatal(err)
	}
	src := netip.MustParseAddrPort("198.18.0.1:40000")
	md := M.Metadata{
		Source:      M.SocksaddrFromNetIP(src),
		Destination: M.SocksaddrFromNetIP(netip.MustParseAddrPort("1.1.1.1:443")),
	}
	init := func(network.PacketConn) network.PacketWriter { return nopWriter{} }
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer := buf.NewSize(1200)
		buffer.Extend(1200)
		h.NewPacket(ctx, src, buffer, md, init)
	}
}

func BenchmarkNewPacketTUN(b *testing.B)    { benchNewPacket(b, C.TUN) }
func BenchmarkNewPacketSOCKS5(b *testing.B) { benchNewPacket(b, C.SOCKS5) }
