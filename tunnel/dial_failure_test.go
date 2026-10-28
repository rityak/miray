package tunnel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	N "github.com/metacubex/mihomo/common/net"
)

// lingerConn is an inbound without a deferred response (TUN through the system
// stack, SOCKS, tunnels:): it records SetLinger.
type lingerConn struct {
	net.Conn
	linger chan int
}

func (c *lingerConn) SetLinger(sec int) error { c.linger <- sec; return nil }

// lazyConn is a lazy TUN connection (T-153): it fails the test if read before the
// outcome of the dial was reported.
type lazyConn struct {
	reportingConn
	t *testing.T
}

func (c *lazyConn) NeedHandshakeForRead() bool {
	select {
	case <-c.success:
		return false
	default:
		return true
	}
}

func (c *lazyConn) Read(b []byte) (int, error) {
	if c.NeedHandshakeForRead() {
		c.t.Error("the inbound was read before the outcome of the dial")
	}
	return c.reportingConn.Read(b)
}

func (c *lazyConn) SetReadDeadline(t time.Time) error {
	if c.NeedHandshakeForRead() {
		c.t.Error("a read deadline was set before the outcome of the dial")
	}
	return c.reportingConn.SetReadDeadline(t)
}

type rejectConn struct{ handshakeConn }

func (c *rejectConn) Chains() C.Chain { return C.Chain{"REJECT"} }

type rejectProxy struct{ handshakeProxy }

func (p *rejectProxy) DialContext(context.Context, *C.Metadata) (C.Conn, error) {
	remote, _ := net.Pipe()
	return &rejectConn{handshakeConn{ExtendedConn: N.NewExtendedConn(remote), wrote: make(chan struct{})}}, nil
}

func runInbound(t *testing.T, proxy C.Proxy, inbound func(net.Conn) net.Conn, port uint16) {
	t.Helper()
	oldProxies, oldMode := proxies, mode.Load()
	t.Cleanup(func() { proxies = oldProxies; mode.Store(oldMode) })
	mode.Store(Direct)
	OnRunning()
	proxies = map[string]C.Proxy{"DIRECT": proxy}
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	dst := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.24"), port)
	metadata := &C.Metadata{NetWork: C.TCP, Type: C.TUN, SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 40002, DstIP: dst.Addr(), DstPort: dst.Port()}
	go Tunnel.HandleTCPConn(inbound(server), metadata)
}

// T-153: an inbound that cannot report a failure itself is reset, not closed
// cleanly, when the dial fails or a REJECT rule refuses (RFC 9293 §3.10.7.1).
func TestDialFailureResetsInbound(t *testing.T) {
	for name, proxy := range map[string]C.Proxy{"failed dial": &failProxy{}, "REJECT": &rejectProxy{}} {
		t.Run(name, func(t *testing.T) {
			linger := make(chan int, 1)
			runInbound(t, proxy, func(c net.Conn) net.Conn { return &lingerConn{Conn: c, linger: linger} }, 81)
			select {
			case sec := <-linger:
				if sec != 0 {
					t.Fatalf("SetLinger(%d), want 0", sec)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the inbound was not reset")
			}
		})
	}
}

// T-153: REJECT reaches an inbound with a deferred response as a failure, and a lazy
// TUN connection is never read before the outcome (no early-data wait for it).
func TestRejectReportedAndLazyNotRead(t *testing.T) {
	for name, proxy := range map[string]C.Proxy{"REJECT": &rejectProxy{}, "failed dial": &failProxy{}} {
		t.Run(name, func(t *testing.T) {
			var c *lazyConn
			runInbound(t, proxy, func(inbound net.Conn) net.Conn {
				c = &lazyConn{reportingConn: reportingConn{Conn: inbound, success: make(chan struct{}), failure: make(chan error, 1)}, t: t}
				return c
			}, 82)
			select {
			case err := <-c.failure:
				if name == "REJECT" && !errors.Is(err, errRejected) {
					t.Fatalf("failure %v, want the rejection", err)
				}
			case <-c.success:
				t.Fatal("success reported for a refused connection")
			case <-time.After(10 * time.Second):
				t.Fatal("failure not reported")
			}
		})
	}
	ok := &handshakeProxy{conns: make(chan *handshakeConn, 1)}
	var c *lazyConn
	start := time.Now()
	runInbound(t, ok, func(inbound net.Conn) net.Conn {
		c = &lazyConn{reportingConn: reportingConn{Conn: inbound, success: make(chan struct{}), failure: make(chan error, 1)}, t: t}
		return c
	}, 83)
	select {
	case <-c.success:
		if d := time.Since(start); d >= earlyDataWait {
			t.Fatalf("success after %v: the dial waited for early data", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("success not reported")
	}
}
