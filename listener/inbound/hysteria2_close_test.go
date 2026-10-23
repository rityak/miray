package inbound_test

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"

	N "github.com/metacubex/sing/common/network"
)

// T-105: a short upload closed at once, while the server is still dialling, arrives. The
// server here reports the dial the way sing-box does and drops the connection when the
// response cannot be written; the client's Close used to cancel the read side, so the
// response failed and the upload was lost.
func TestInboundHysteria2UploadThenCloseBeforeResponse(t *testing.T) {
	in, err := inbound.NewHysteria2(&inbound.Hysteria2Option{
		BaseOption:  inbound.BaseOption{NameStr: "hysteria2_close_inbound", Listen: "127.0.0.1", Port: "0"},
		Users:       map[string]string{"test": userUUID},
		Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	got := make(chan int64, 1)
	tunnel.HandleTCPConnFn = func(conn net.Conn, _ *C.Metadata) {
		defer conn.Close()
		time.Sleep(200 * time.Millisecond) // the dial; the client has closed by now
		if err := N.ReportHandshakeSuccess(conn); err != nil {
			got <- -1 // sing-box drops the connection here
			return
		}
		n, _ := io.Copy(io.Discard, conn)
		got <- n
	}
	if err := in.Listen(tunnel); err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	addrPort := netip.MustParseAddrPort(in.Address())
	opts := outbound.Hysteria2Option{Name: "hysteria2_close_outbound", Server: addrPort.Addr().String(), Port: int(addrPort.Port()),
		Password: userUUID, Fingerprint: tlsFingerprint}
	opts.DialerForAPI, opts.TunnelForAPI = tunnel.NewDialer(), tunnel
	out, err := outbound.NewHysteria2(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	const size = 32 << 10
	for i := 0; i < 3; i++ {
		conn, err := out.DialContext(context.Background(), &C.Metadata{NetWork: C.TCP, DstIP: netip.MustParseAddr("192.0.2.1"), DstPort: 443})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(make([]byte, size)); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		select {
		case n := <-got:
			if n != size {
				t.Fatalf("upload %d: the server got %d of %d bytes (-1: its response could not be written)", i, n, size)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("upload %d: the server did not see the end of it", i)
		}
	}
}
