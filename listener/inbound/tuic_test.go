package inbound_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"

	"github.com/stretchr/testify/assert"
)

var tuicCCs = []string{"cubic", "new_reno", "bbr"}

func testInboundTuic(t *testing.T, inboundOptions inbound.TuicOption, outboundOptions outbound.TuicOption) {
	t.Parallel()
	inboundOptions.Users = map[string]string{userUUID: userUUID}
	inboundOptions.Token = []string{userUUID}

	for _, tuicCC := range tuicCCs {
		tuicCC := tuicCC
		t.Run(tuicCC, func(t *testing.T) {
			t.Parallel()
			t.Run("v4", func(t *testing.T) {
				inboundOptions, outboundOptions := inboundOptions, outboundOptions // don't modify outside options value
				outboundOptions.Token = userUUID
				outboundOptions.CongestionController = tuicCC
				inboundOptions.CongestionController = tuicCC
				testInboundTuic0(t, inboundOptions, outboundOptions)
			})
			t.Run("v5", func(t *testing.T) {
				inboundOptions, outboundOptions := inboundOptions, outboundOptions // don't modify outside options value
				outboundOptions.UUID = userUUID
				outboundOptions.Password = userUUID
				outboundOptions.CongestionController = tuicCC
				inboundOptions.CongestionController = tuicCC
				testInboundTuic0(t, inboundOptions, outboundOptions)
			})
		})
	}
}

func testInboundTuic0(t *testing.T, inboundOptions inbound.TuicOption, outboundOptions outbound.TuicOption) {
	t.Parallel()
	inboundOptions.BaseOption = inbound.BaseOption{
		NameStr: "tuic_inbound",
		Listen:  "127.0.0.1",
		Port:    "0",
	}
	in, err := inbound.NewTuic(&inboundOptions)
	if !assert.NoError(t, err) {
		return
	}

	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()

	err = in.Listen(tunnel)
	if !assert.NoError(t, err) {
		return
	}
	defer in.Close()

	addrPort, err := netip.ParseAddrPort(in.Address())
	if !assert.NoError(t, err) {
		return
	}

	outboundOptions.Name = "tuic_outbound"
	outboundOptions.Server = addrPort.Addr().String()
	outboundOptions.Port = int(addrPort.Port())
	outboundOptions.DialerForAPI = tunnel.NewDialer()
	outboundOptions.TunnelForAPI = tunnel

	out, err := outbound.NewTuic(outboundOptions)
	if !assert.NoError(t, err) {
		return
	}
	defer out.Close()

	tunnel.DoTest(t, out)
}

func TestInboundTuic_TLS(t *testing.T) {
	inboundOptions := inbound.TuicOption{
		Certificate:           tlsCertificate,
		PrivateKey:            tlsPrivateKey,
		AuthenticationTimeout: 5000,
	}
	outboundOptions := outbound.TuicOption{
		Fingerprint: tlsFingerprint,
	}
	testInboundTuic(t, inboundOptions, outboundOptions)
	if winGo120 {
		return // subsequent tests frequently fail; skipping them for now.
	}
	t.Run("ECH", func(t *testing.T) {
		inboundOptions := inboundOptions
		outboundOptions := outboundOptions
		inboundOptions.EchKey = echKeyPem
		outboundOptions.ECHOpts = outbound.ECHOptions{
			Enable: true,
			Config: echConfigBase64,
		}
		testInboundTuic(t, inboundOptions, outboundOptions)
	})
	t.Run("mTLS", func(t *testing.T) {
		inboundOptions := inboundOptions
		outboundOptions := outboundOptions
		inboundOptions.ClientAuthCert = tlsAuthCertificate
		outboundOptions.Certificate = tlsAuthCertificate
		outboundOptions.PrivateKey = tlsAuthPrivateKey
		testInboundTuic(t, inboundOptions, outboundOptions)
	})
	t.Run("mTLS+ECH", func(t *testing.T) {
		inboundOptions := inboundOptions
		outboundOptions := outboundOptions
		inboundOptions.ClientAuthCert = tlsAuthCertificate
		outboundOptions.Certificate = tlsAuthCertificate
		outboundOptions.PrivateKey = tlsAuthPrivateKey
		inboundOptions.EchKey = echKeyPem
		outboundOptions.ECHOpts = outbound.ECHOptions{
			Enable: true,
			Config: echConfigBase64,
		}
		testInboundTuic(t, inboundOptions, outboundOptions)
	})
}

// T-064: datagrams of one flow keep their order through tuic (native UDP relay).
func TestInboundTuicUDPOrder(t *testing.T) {
	for _, version := range []string{"v4", "v5"} {
		t.Run(version, func(t *testing.T) {
			in, err := inbound.NewTuic(&inbound.TuicOption{
				BaseOption:  inbound.BaseOption{NameStr: "tuic_udp_inbound", Listen: "127.0.0.1", Port: "0"},
				Users:       map[string]string{userUUID: userUUID},
				Token:       []string{userUUID},
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
			outOptions := outbound.TuicOption{
				Name: "tuic_udp_outbound", Server: addrPort.Addr().String(), Port: int(addrPort.Port()),
				Fingerprint: tlsFingerprint, UdpRelayMode: "native",
			}
			if version == "v4" {
				outOptions.Token = userUUID
			} else {
				outOptions.UUID, outOptions.Password = userUUID, userUUID
			}
			outOptions.DialerForAPI = tunnel.NewDialer()
			outOptions.TunnelForAPI = tunnel
			out, err := outbound.NewTuic(outOptions)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			testUDPEchoOrder(t, tunnel, out)
		})
	}
}

// T-081: datagrams are not cut at the old safe size any more; ones over what the
// path takes still arrive whole through fragments, also before MTU discovery.
func TestInboundTuicUDPLarge(t *testing.T) {
	in, err := inbound.NewTuic(&inbound.TuicOption{
		BaseOption:  inbound.BaseOption{NameStr: "tuic_large_inbound", Listen: "127.0.0.1", Port: "0"},
		Users:       map[string]string{userUUID: userUUID},
		Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	tunnel.HandleUDPPacketFn = func(packet C.UDPPacket, metadata *C.Metadata) {
		_, _ = packet.WriteBack(packet.Data(), metadata.UDPAddr())
		packet.Drop()
	}
	if err := in.Listen(tunnel); err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	addrPort := netip.MustParseAddrPort(in.Address())
	outOptions := outbound.TuicOption{
		Name: "tuic_large_outbound", Server: addrPort.Addr().String(), Port: int(addrPort.Port()),
		Fingerprint: tlsFingerprint, UdpRelayMode: "native", UUID: userUUID, Password: userUUID,
	}
	outOptions.DialerForAPI = tunnel.NewDialer()
	outOptions.TunnelForAPI = tunnel
	out, err := outbound.NewTuic(outOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	dst := netip.MustParseAddrPort("192.0.2.1:53")
	pc, err := out.ListenPacketContext(context.Background(), &C.Metadata{NetWork: C.UDP, DstIP: dst.Addr(), DstPort: dst.Port()})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	buf := make([]byte, 4096)
	for _, size := range []int{1100, 1300, 1500, 3000} {
		want := bytes.Repeat([]byte{byte(size)}, size)
		got := false
		for try := 0; try < 5 && !got; try++ { // a datagram may be lost; a corrupt one fails at once
			if _, err := pc.WriteTo(want, net.UDPAddrFromAddrPort(dst)); err != nil {
				t.Fatalf("%d bytes: %v", size, err)
			}
			_ = pc.SetReadDeadline(time.Now().Add(time.Second))
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				continue
			}
			if !bytes.Equal(buf[:n], want) {
				t.Fatalf("%d bytes sent, %d came back or corrupt", size, n)
			}
			got = true
		}
		if !got {
			t.Fatalf("%d bytes: no echo", size)
		}
	}
}
