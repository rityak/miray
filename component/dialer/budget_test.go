package dialer

import (
	"context"
	"net"
	"net/netip"
	"strconv"
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

// A blackholed first address must leave budget for the next one (T-011).
func TestSerialDialSplitsBudget(t *testing.T) {
	port := listen(t, "tcp4", "127.0.0.1:0")
	blackhole := netip.MustParseAddr("127.0.0.2")
	d, _, _ := probeDialer(blackhole)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := serialDialContext(ctx, "tcp4", []netip.Addr{blackhole, netip.MustParseAddr("127.0.0.1")}, port, option{netDialer: d})
	if err != nil {
		t.Fatalf("dial failed after %v: %v", time.Since(start), err)
	}
	_ = conn.Close()
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("took %v, want about half of the 2s budget", elapsed)
	}
}

// With a preferred family that answers, the other family is never dialed.
func TestStaggeredDialSkipsFallback(t *testing.T) {
	port := listen(t, "tcp4", "127.0.0.1:0")
	d, attempts, _ := probeDialer(netip.Addr{})
	conn, err := dualStackDialContext(context.Background(), serialDialContext, "tcp",
		[]netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, port, option{netDialer: d, prefer: 4})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	time.Sleep(dualStackFallbackTimeout + 100*time.Millisecond)
	if got := attempts(); len(got) != 1 {
		t.Fatalf("attempts = %v, want only the preferred family", got)
	}
}

// A silent preferred family hands over after the fallback delay, and its
// pending attempt is canceled.
func TestStaggeredDialFallsBack(t *testing.T) {
	port := listen(t, "tcp6", "[::1]:0")
	blackhole := netip.MustParseAddr("127.0.0.1")
	d, _, canceled := probeDialer(blackhole)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := dualStackDialContext(ctx, serialDialContext, "tcp",
		[]netip.Addr{blackhole, netip.MustParseAddr("::1")}, port, option{netDialer: d, prefer: 4})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if elapsed := time.Since(start); elapsed < dualStackFallbackTimeout || elapsed > time.Second {
		t.Fatalf("fallback after %v, want just over %v", elapsed, dualStackFallbackTimeout)
	}
	for i := 0; !canceled() && i < 100; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !canceled() {
		t.Fatal("the losing attempt was not canceled")
	}
}
