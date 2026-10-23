package common

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/quic-go"
	"github.com/metacubex/tls"
)

type udpDialer struct{}

func (udpDialer) ListenPacket(context.Context, string, string, netip.AddrPort) (net.PacketConn, error) {
	return net.ListenUDP("udp", nil)
}

// A server whose port is closed (it is restarting) answers with ICMP port unreachable: the client learns
// it from the first datagram instead of waiting out the handshake or idle timeout (T-116).
func TestDialQuicClosedPortFailsAtOnce(t *testing.T) {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := l.LocalAddr().String()
	_ = l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err = DialQuic(ctx, addr, nil, udpDialer{}, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}},
		&quic.Config{HandshakeIdleTimeout: 4 * time.Second}, DialQuicOption{})
	if err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) || time.Since(start) > time.Second {
		t.Fatalf("after %v: %v; want connection refused at once", time.Since(start).Round(time.Millisecond), err)
	}
}
