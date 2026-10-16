package inbound_test

import (
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/listener/inbound"
)

// T-063: datagrams of one flow keep their order through hysteria2.
func TestInboundHysteria2UDPOrder(t *testing.T) {
	in, err := inbound.NewHysteria2(&inbound.Hysteria2Option{
		BaseOption:  inbound.BaseOption{NameStr: "hysteria2_udp_inbound", Listen: "127.0.0.1", Port: "0"},
		Users:       map[string]string{"test": userUUID},
		Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	if err := in.Listen(tunnel); err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	addrPort := netip.MustParseAddrPort(in.Address())
	outOptions := outbound.Hysteria2Option{
		Name: "hysteria2_udp_outbound", Server: addrPort.Addr().String(), Port: int(addrPort.Port()),
		Password: userUUID, Fingerprint: tlsFingerprint,
	}
	outOptions.DialerForAPI = tunnel.NewDialer()
	outOptions.TunnelForAPI = tunnel
	out, err := outbound.NewHysteria2(outOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	testUDPEchoOrder(t, tunnel, out)
}
