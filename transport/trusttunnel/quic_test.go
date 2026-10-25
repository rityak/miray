package trusttunnel

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/ca"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/transport/vmess"

	"github.com/metacubex/sing/common/logger"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
	"github.com/metacubex/tls"
)

type echoHandler struct{}

func (echoHandler) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {
	defer conn.Close()
	_, err := io.Copy(conn, conn)
	return err
}

func (echoHandler) NewPacketConnection(ctx context.Context, conn network.PacketConn, metadata M.Metadata) error {
	return conn.Close()
}

type testDialer struct{}

func (testDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return new(net.Dialer).DialContext(ctx, network, address)
}

func (testDialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	return net.ListenUDP("udp", nil)
}

// T-141: a CONNECT stream with no traffic outlives the QUIC idle timeout. The custom
// QUIC config used to drop the keep-alive of quic-go's HTTP/3 default config, so the
// connection closed by idle timeout with its open streams.
func TestQUICIdleStreamKeptAlive(t *testing.T) {
	quicMaxIdleTimeout = time.Second
	t.Cleanup(func() { quicMaxIdleTimeout = DefaultQuicMaxIdleTimeout })

	certificate, privateKey, fingerprint, err := ca.NewRandomTLSKeyPair(ca.KeyPairTypeP256)
	if err != nil {
		t.Fatal(err)
	}
	keyPair, err := tls.X509KeyPair([]byte(certificate), []byte(privateKey))
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewService(ServiceOptions{Ctx: ctx, Logger: logger.NOP(), Handler: echoHandler{}})
	service.UpdateUsers(map[string]string{"user": "password"})
	if err := service.Start(nil, udpConn, &tls.Config{Certificates: []tls.Certificate{keyPair}}); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	client, err := NewClient(ctx, ClientOptions{
		Dialer:      testDialer{},
		DialOptions: func() []dialer.Option { return nil },
		Server:      udpConn.LocalAddr().String(),
		Username:    "user",
		Password:    "password",
		TLSConfig:   &vmess.TLSConfig{Host: "example.org", FingerPrint: fingerprint},
		QUIC:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := client.Dial(ctx, "example.org:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	echo := func(b byte) error {
		result := make(chan error, 1)
		go func() {
			if _, err := conn.Write([]byte{b}); err != nil {
				result <- err
				return
			}
			buf := make([]byte, 1)
			_, err := io.ReadFull(conn, buf)
			result <- err
		}()
		select {
		case err := <-result:
			return err
		case <-time.After(2 * time.Second):
			return context.DeadlineExceeded
		}
	}
	if err := echo('a'); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * quicMaxIdleTimeout)
	if err := echo('b'); err != nil {
		t.Fatalf("idle stream did not survive the QUIC idle timeout: %v", err)
	}
}
