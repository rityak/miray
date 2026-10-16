package inbound_test

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"
)

// UDP through a shadowsocks inbound: a burst of datagrams (read in batches by
// the listener on Linux, T-060) comes back intact, each exactly once.
func TestInboundShadowSocksUDP(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0") // a port free for udp; the inbound also needs it for tcp
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()

	const cipher = "2022-blake3-aes-128-gcm"
	in, err := inbound.NewShadowSocks(&inbound.ShadowSocksOption{
		BaseOption: inbound.BaseOption{NameStr: "shadowsocks_udp_inbound", Listen: "127.0.0.1", Port: strconv.Itoa(port)},
		Password:   shadowsocksPassword16, Cipher: cipher, UDP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	tunnel.HandleUDPPacketFn = func(packet C.UDPPacket, metadata *C.Metadata) {
		_, _ = packet.WriteBack(packet.Data(), metadata.UDPAddr()) // echo
		packet.Drop()
	}
	if err := in.Listen(tunnel); err != nil {
		t.Skip("listen:", err) // the port was taken for tcp in between
	}
	defer in.Close()

	out, err := outbound.NewShadowSocks(outbound.ShadowSocksOption{
		Name: "shadowsocks_udp_outbound", Server: "127.0.0.1", Port: port,
		Password: shadowsocksPassword16, Cipher: cipher, UDP: true,
	})
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

	const count = 64
	for i := 0; i < count; i++ {
		d := make([]byte, 3+i%7) // sizes differ, so a mixed-up length in a batch shows
		for j := range d {
			d[j] = byte(i)
		}
		if _, err := pc.WriteTo(d, net.UDPAddrFromAddrPort(dst)); err != nil {
			t.Fatal(err)
		}
	}
	_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	seen := make(map[byte]bool)
	buf := make([]byte, 1500)
	for len(seen) < count {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatalf("after %d of %d echoes: %v", len(seen), count, err)
		}
		if n == 0 || n != 3+int(buf[0])%7 || seen[buf[0]] {
			t.Fatalf("bad or repeated echo %v", buf[:n])
		}
		for _, b := range buf[:n] {
			if b != buf[0] {
				t.Fatalf("corrupt echo %v", buf[:n])
			}
		}
		seen[buf[0]] = true
	}
}
