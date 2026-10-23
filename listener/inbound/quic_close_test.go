package inbound_test

import (
	"context"
	"errors"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/listener/inbound"

	"github.com/metacubex/quic-go"
	"github.com/metacubex/tls"
)

// T-119: a QUIC inbound that closes (shutdown, reload) closes its connections with
// CONNECTION_CLOSE, as sing-box does, so a client reconnects at once instead of
// waiting for the idle timeout. The client is a bare QUIC connection.
func TestInboundQUICCloseNotifiesClients(t *testing.T) {
	cases := map[string]func() (C.InboundListener, error){
		"hysteria2": func() (C.InboundListener, error) {
			return inbound.NewHysteria2(&inbound.Hysteria2Option{
				BaseOption: inbound.BaseOption{NameStr: "hy2_close_inbound", Listen: "127.0.0.1", Port: "0"},
				Users:      map[string]string{"test": userUUID}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
			})
		},
		"tuic": func() (C.InboundListener, error) {
			return inbound.NewTuic(&inbound.TuicOption{
				BaseOption: inbound.BaseOption{NameStr: "tuic_close_inbound", Listen: "127.0.0.1", Port: "0"},
				Users:      map[string]string{userUUID: userUUID}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
			})
		},
		"trusttunnel": func() (C.InboundListener, error) {
			return inbound.NewTrustTunnel(&inbound.TrustTunnelOption{
				BaseOption: inbound.BaseOption{NameStr: "tt_close_inbound", Listen: "127.0.0.1", Port: "0"},
				Users:      []inbound.AuthUser{{Username: "u", Password: userUUID}}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
				Network: []string{"udp"},
			})
		},
	}
	for name, newInbound := range cases {
		t.Run(name, func(t *testing.T) {
			in, err := newInbound()
			if err != nil {
				t.Fatal(err)
			}
			tunnel := NewHttpTestTunnel()
			defer tunnel.Close()
			if err := in.Listen(tunnel); err != nil {
				t.Fatal(err)
			}
			closed := false
			defer func() {
				if !closed {
					in.Close()
				}
			}()
			expectServerClose(t, in.Address(), func() { in.Close(); closed = true })
		})
	}
}

// On shutdown the inbounds of the config (listeners:) close too, not only the TUN.
func TestCleanupClosesInboundListeners(t *testing.T) {
	in, err := inbound.NewHysteria2(&inbound.Hysteria2Option{
		BaseOption: inbound.BaseOption{NameStr: "hy2_cleanup_inbound", Listen: "127.0.0.1", Port: "0"},
		Users:      map[string]string{"test": userUUID}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	listener.PatchInboundListeners(map[string]C.InboundListener{"hy2_cleanup_inbound": in}, tunnel, false)
	expectServerClose(t, in.Address(), listener.Cleanup)
}

// expectServerClose connects a bare QUIC client, runs closeServer and checks that the
// client learns of it from the server's CONNECTION_CLOSE, not from its idle timeout.
func expectServerClose(t *testing.T, addr string, closeServer func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}, ServerName: "example.org"},
		&quic.Config{MaxIdleTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")
	time.Sleep(200 * time.Millisecond) // the server accepts the connection after the handshake
	closeServer()
	select {
	case <-conn.Context().Done():
		var appErr *quic.ApplicationError
		if cause := context.Cause(conn.Context()); !errors.As(cause, &appErr) || !appErr.Remote {
			t.Fatalf("connection ended with %v, want the server's CONNECTION_CLOSE", cause)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the client did not learn that the server closed")
	}
}
