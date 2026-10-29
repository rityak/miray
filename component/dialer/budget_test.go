package dialer

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// probeDialer blocks dials to the blackhole address until their context ends,
// like an unanswered SYN, and records every attempted address.
func probeDialer(blackhole netip.Addr) (*net.Dialer, func() []string, func() bool) {
	var mu sync.Mutex
	var attempts []string
	canceled := false
	d := &net.Dialer{ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
		mu.Lock()
		attempts = append(attempts, address)
		mu.Unlock()
		if ap, err := netip.ParseAddrPort(address); err == nil && ap.Addr() == blackhole {
			<-ctx.Done()
			mu.Lock()
			canceled = true
			mu.Unlock()
			return ctx.Err()
		}
		return nil
	}}
	return d,
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), attempts...) },
		func() bool { mu.Lock(); defer mu.Unlock(); return canceled }
}

func listen(t *testing.T, network, addr string) string {
	t.Helper()
	l, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("listen %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// refusingDialer refuses the refused address at once, like a closed port, and
// records when each address was attempted.
func refusingDialer(refused netip.Addr) (*net.Dialer, func() map[string]time.Time) {
	var mu sync.Mutex
	at := map[string]time.Time{}
	d := &net.Dialer{ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
		mu.Lock()
		at[address] = time.Now()
		mu.Unlock()
		if ap, err := netip.ParseAddrPort(address); err == nil && ap.Addr() == refused {
			return syscall.ECONNREFUSED
		}
		return nil
	}}
	return d, func() map[string]time.Time { mu.Lock(); defer mu.Unlock(); return at }
}

// A silent first address hands over after the Connection Attempt Delay, not a
// share of the timeout, and its attempt is canceled once the next one wins
// (RFC 8305 §5, T-155); the same across families, the preferred one first.
func TestHappyEyeballsSilentFirst(t *testing.T) {
	for _, c := range []struct {
		name, network, listenOn string
		ips                     []netip.Addr
		prefer                  int
	}{
		{"one family", "tcp4", "127.0.0.1:0", []netip.Addr{netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("127.0.0.1")}, 0},
		{"ipv4 preferred", "tcp", "[::1]:0", []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			port := listen(t, c.network, c.listenOn)
			d, _, canceled := probeDialer(c.ips[0])
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			start := time.Now()
			conn, err := happyEyeballsDial(ctx, c.network, happyEyeballsOrder(c.ips, c.prefer), port, option{netDialer: d})
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			if elapsed := time.Since(start); elapsed < connectionAttemptDelay || elapsed > connectionAttemptDelay+300*time.Millisecond {
				t.Fatalf("connected after %v, want just over %v", elapsed, connectionAttemptDelay)
			}
			for i := 0; !canceled() && i < 100; i++ {
				time.Sleep(5 * time.Millisecond)
			}
			if !canceled() {
				t.Fatal("the losing attempt was not canceled")
			}
		})
	}
}

// A refused attempt starts the next one at once, without the delay (RFC 8305 §5).
func TestHappyEyeballsRefusedStartsNext(t *testing.T) {
	port := listen(t, "tcp4", "127.0.0.1:0")
	refused := netip.MustParseAddr("127.0.0.2")
	d, at := refusingDialer(refused)
	start := time.Now()
	conn, err := happyEyeballsDial(context.Background(), "tcp4", []netip.Addr{refused, netip.MustParseAddr("127.0.0.1")}, port, option{netDialer: d})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if elapsed := time.Since(start); elapsed >= connectionAttemptDelay {
		t.Fatalf("connected after %v: the next attempt waited for the delay", elapsed)
	}
	if len(at()) != 2 {
		t.Fatalf("attempts %v", at())
	}
}

// An address that answers within the delay is the only one dialed.
func TestHappyEyeballsFirstAnswers(t *testing.T) {
	port := listen(t, "tcp4", "127.0.0.1:0")
	d, attempts, _ := probeDialer(netip.Addr{})
	conn, err := happyEyeballsDial(context.Background(), "tcp",
		happyEyeballsOrder([]netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, 4), port, option{netDialer: d})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	time.Sleep(connectionAttemptDelay + 100*time.Millisecond)
	if got := attempts(); len(got) != 1 {
		t.Fatalf("attempts = %v, want only the first", got)
	}
}

// All attempts failing returns every error, in the order tried.
func TestHappyEyeballsAllFail(t *testing.T) {
	a, b := netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("127.0.0.3")
	d := &net.Dialer{ControlContext: func(context.Context, string, string, syscall.RawConn) error { return syscall.ECONNREFUSED }}
	_, err := happyEyeballsDial(context.Background(), "tcp4", []netip.Addr{a, b}, "1", option{netDialer: d})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.2") || !strings.Contains(err.Error(), "127.0.0.3") ||
		strings.Index(err.Error(), "127.0.0.2") > strings.Index(err.Error(), "127.0.0.3") {
		t.Fatalf("error %v, want both attempts in order", err)
	}
}

// The families alternate, IPv6 first unless IPv4 is preferred (RFC 8305 §4).
func TestHappyEyeballsOrder(t *testing.T) {
	ips := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("192.0.2.3"),
		netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")}
	for prefer, want := range map[int]string{
		0: "[2001:db8::1 192.0.2.1 2001:db8::2 192.0.2.2 192.0.2.3]",
		6: "[2001:db8::1 192.0.2.1 2001:db8::2 192.0.2.2 192.0.2.3]",
		4: "[192.0.2.1 2001:db8::1 192.0.2.2 2001:db8::2 192.0.2.3]",
	} {
		if got := fmt.Sprint(happyEyeballsOrder(ips, prefer)); got != want {
			t.Errorf("prefer %d: %s, want %s", prefer, got, want)
		}
	}
}
