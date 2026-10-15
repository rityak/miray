package dialer

import (
	"context"
	"io"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
)

type fixedDialResolver struct {
	resolver.Resolver
	addresses []netip.Addr
}

func (r fixedDialResolver) Invalid() bool { return true }

func (r fixedDialResolver) LookupIP(context.Context, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addresses...), nil
}

func (r fixedDialResolver) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return r.LookupIP(ctx, host)
}

func TestDialContextCancelsLosingStandardDial(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "dual-stack", true: "parallel"}[concurrent], func(t *testing.T) {
			previous := GetTcpConcurrent()
			SetTcpConcurrent(concurrent)
			defer SetTcpConcurrent(previous)

			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started := make(chan struct{})
			canceled := make(chan struct{})
			standardDialer := &net.Dialer{ControlContext: func(ctx context.Context, _, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if host == "127.0.0.1" {
					select {
					case <-started:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				close(started)
				<-ctx.Done()
				close(canceled)
				return ctx.Err()
			}}
			addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.IPv6Loopback()}
			network := "tcp"
			if concurrent {
				addresses[1] = netip.MustParseAddr("127.0.0.2")
				network = "tcp4"
			}
			conn, err := DialContext(ctx, network, net.JoinHostPort("race.test", port),
				WithResolver(fixedDialResolver{addresses: addresses}), WithNetDialer(standardDialer))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("losing dial remained active after a connection was returned")
			}

			peer, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			if _, err = peer.Write([]byte("ok")); err != nil {
				t.Fatal(err)
			}
			if err = conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, 2)
			if _, err = io.ReadFull(conn, payload); err != nil || string(payload) != "ok" {
				t.Fatalf("returned connection unusable after dial cancellation: %q, %v", payload, err)
			}
		})
	}
}

func TestDialContextPreservesCustomDialContext(t *testing.T) {
	previous := GetTcpConcurrent()
	SetTcpConcurrent(true)
	defer SetTcpConcurrent(previous)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	var winnerContext context.Context
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	customDialer := NetDialerFunc(func(ctx context.Context, _, address string) (net.Conn, error) {
		if address == "127.0.0.1:80" {
			<-started
			winnerContext = ctx
			return left, nil
		}
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	conn, err := DialContext(ctx, "tcp4", "race.test:80",
		WithResolver(fixedDialResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}}),
		WithNetDialer(customDialer))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if winnerContext != ctx || winnerContext.Err() != nil {
		t.Fatal("custom dialer's returned stream context was changed or canceled")
	}
}
