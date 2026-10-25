package outboundgroup

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/component/iface"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"

	"github.com/stretchr/testify/require"
)

// rnode is a node on a server address with a connect latency and a failure kind.
type rnode struct {
	*outbound.Base
	latency atomic.Int64 // time.Duration
	err     atomic.Value // error; nil connects
	dials   atomic.Int32
}

var (
	errHostDown = &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}                                          // no answer to the SYN
	errRefused  = &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}} // the server answered
)

func newRNode(name, addr string, latency time.Duration, err error) *rnode {
	n := &rnode{Base: outbound.NewBase(outbound.BaseOption{Name: name, Addr: addr, Type: C.Vless, UDP: true})}
	n.latency.Store(int64(latency))
	n.setErr(err)
	return n
}

type errBox struct{ err error }

func (n *rnode) setErr(err error) { n.err.Store(errBox{err}) }

func (n *rnode) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	n.dials.Add(1)
	select {
	case <-time.After(time.Duration(n.latency.Load())):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := n.err.Load().(errBox).err; err != nil {
		return nil, err
	}
	c, _ := net.Pipe()
	return outbound.NewConn(c, n), nil
}

func (n *rnode) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	return nil, C.ErrNotSupport
}

func raceProviders(t *testing.T, nodes ...*rnode) []P.ProxyProvider {
	t.Helper()
	proxies := make([]C.Proxy, len(nodes))
	for i, n := range nodes {
		proxies[i] = adapter.NewProxy(n)
	}
	pd, err := provider.NewCompatibleProvider("pd", proxies, provider.NewHealthCheck(proxies, "", 0, 0, false, nil))
	require.NoError(t, err)
	return []P.ProxyProvider{pd}
}

func fastRaces(t *testing.T) {
	t.Helper()
	min, def := raceDelayMin, raceDelayDefault
	raceDelayMin, raceDelayDefault = 10*time.Millisecond, 30*time.Millisecond
	t.Cleanup(func() { raceDelayMin, raceDelayDefault = min, def })
}

type chooser interface {
	C.ProxyAdapter
	Unwrap(metadata *C.Metadata, touch bool) C.Proxy
}

func raceGroups(t *testing.T, nodes ...*rnode) map[string]chooser {
	t.Helper()
	empty := adapter.NewProxy(outbound.NewReject())
	opt := GroupCommonOption{Name: "g", TestTimeout: 5000, MaxFailedTimes: 5}
	fb, err := NewFallback(opt, FallbackOption{}, empty, raceProviders(t, nodes...))
	require.NoError(t, err)
	ut, err := NewURLTest(opt, URLTestOption{}, empty, raceProviders(t, nodes...))
	require.NoError(t, err)
	lb, err := NewLoadBalance(opt, LoadBalanceOption{Strategy: "consistent-hashing"}, empty, raceProviders(t, nodes...))
	require.NoError(t, err)
	return map[string]chooser{"fallback": fb, "url-test": ut, "load-balance": lb}
}

// A node that is only slower than usual (jitter, a lagging provider) loses the race for
// this one connection and nothing more: it is not set aside, and fallback's order,
// url-test's choice and load-balance's hash still pick it (T-135, the user's condition).
func TestRaceSlowNodeIsNotAFailure(t *testing.T) {
	fastRaces(t)
	for _, name := range []string{"fallback", "url-test", "load-balance"} {
		t.Run(name, func(t *testing.T) {
			slow := newRNode("slow", "192.0.2.1:443", 150*time.Millisecond, nil)
			quick := newRNode("quick", "192.0.2.2:443", 0, nil)
			g := raceGroups(t, slow, quick)[name]
			var m *C.Metadata // a target the group sends to the slow node (load-balance hashes it)
			for i := 0; i < 64 && m == nil; i++ {
				cand := &C.Metadata{NetWork: C.TCP, Host: fmt.Sprintf("example%d.com", i), DstPort: 443}
				if g.Unwrap(cand, false).Name() == "slow" {
					m = cand
				}
			}
			require.NotNil(t, m, "no target goes to the slow node")
			c, err := g.DialContext(context.Background(), m)
			require.NoError(t, err)
			_ = c.Close()
			require.EqualValues(t, 1, quick.dials.Load(), "no race against the slow node")

			require.Equal(t, "slow", g.Unwrap(m, false).Name(), "the group's choice moved to the race winner")
			before := slow.dials.Load()
			c, err = g.DialContext(context.Background(), m)
			require.NoError(t, err)
			_ = c.Close()
			require.Greater(t, slow.dials.Load(), before, "the slow node was set aside after losing a race")
		})
	}
}

// A dead server loses the race long before its SYN times out: max-failed-times races lost
// in a row start the group's health check. A node that only lags now and then (it wins
// in between) never does.
func TestLostRacesInARowStartHealthCheck(t *testing.T) {
	fastRaces(t)
	for _, tc := range []struct {
		name   string
		slowAt func(i int) bool
		checks int32
	}{
		{"dead server", func(int) bool { return true }, 2},
		{"jitter", func(i int) bool { return i%3 != 2 }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newRNode("a", "192.0.2.1:443", 0, nil)
			b := newRNode("b", "192.0.2.2:443", 0, nil)
			g := raceGroups(t, a, b)["fallback"].(*Fallback)
			var checks atomic.Int32
			for i := 0; i < 2*g.maxFailedTimes; i++ {
				lat := time.Duration(0)
				if tc.slowAt(i) {
					lat = 10 * time.Second
				}
				a.latency.Store(int64(lat))
				c, node, err := failover(context.Background(), g.GroupBase, g.findAliveProxy(false), g.testUrl, g.next,
					func(ctx context.Context, p C.Proxy) (C.Conn, error) { return p.DialContext(ctx, dst) },
					nil, func() { checks.Add(1) })
				require.NoError(t, err)
				_ = c.Close()
				require.Equal(t, tc.slowAt(i), node.Name() == "b")
			}
			require.Eventually(t, func() bool { return checks.Load() == tc.checks }, time.Second, 10*time.Millisecond)
			require.Equal(t, "a", g.Now(), "a lost race moved the group")
		})
	}
}

// A node whose server is not reached at all: the retry goes to a node on another server
// rather than to a sibling protocol on the same IP, and once that got through the whole
// server is set aside (S-018).
func TestHostDownGoesToAnotherServer(t *testing.T) {
	fastRaces(t)
	a := newRNode("vps-vless", "198.51.100.7:443", 0, errHostDown)
	sib := newRNode("vps-hy2", "198.51.100.7:8443", 0, errHostDown)
	other := newRNode("other", "203.0.113.9:443", 0, nil)
	g := raceGroups(t, a, sib, other)["fallback"]
	c, err := g.DialContext(context.Background(), dst)
	require.NoError(t, err)
	_ = c.Close()
	require.EqualValues(t, 0, sib.dials.Load(), "the retry went to the same dead server")
	require.EqualValues(t, 1, other.dials.Load())

	c, err = g.DialContext(context.Background(), dst)
	require.NoError(t, err)
	_ = c.Close()
	require.EqualValues(t, 1, a.dials.Load(), "the failed node was tried again")
	require.EqualValues(t, 0, sib.dials.Load(), "the sibling on the dead server was tried")
}

// A server that answered (a refused port, a TLS or protocol error) is alive: the retry may
// take a sibling protocol on it, and only the failed node is set aside.
func TestEndpointFailureKeepsTheServer(t *testing.T) {
	fastRaces(t)
	a := newRNode("vps-vless", "198.51.100.7:443", 0, errRefused)
	sib := newRNode("vps-hy2", "198.51.100.7:8443", 0, nil)
	other := newRNode("other", "203.0.113.9:443", 0, nil)
	g := raceGroups(t, a, sib, other)["fallback"]
	for i := 0; i < 2; i++ {
		c, err := g.DialContext(context.Background(), dst)
		require.NoError(t, err)
		_ = c.Close()
	}
	require.EqualValues(t, 1, a.dials.Load())
	require.EqualValues(t, 1, sib.dials.Load(), "the retry skipped the live server")
	require.EqualValues(t, 1, other.dials.Load(), "the sibling was set aside with the failed node")
}

// A SYN dropped on one port looks like a dead host; with nothing else in the group the
// sibling on the same IP is still tried, and when it gets through only the failed node is
// set aside.
func TestHostDownWithOnlyTheSameServer(t *testing.T) {
	fastRaces(t)
	a := newRNode("vps-vless", "198.51.100.7:443", 0, errHostDown)
	sib := newRNode("vps-hy2", "198.51.100.7:8443", 0, nil)
	g := raceGroups(t, a, sib)["fallback"]
	for i := 0; i < 2; i++ {
		c, err := g.DialContext(context.Background(), dst)
		require.NoError(t, err)
		_ = c.Close()
	}
	require.EqualValues(t, 1, a.dials.Load())
	require.EqualValues(t, 2, sib.dials.Load())
}

// A change of network (or a config reload) makes what the group learned stale: the nodes
// set aside come back (RFC 6555 §4.2).
func TestNetworkChangeClearsSetAside(t *testing.T) {
	fastRaces(t)
	a := newRNode("a", "192.0.2.1:443", 0, errRefused)
	b := newRNode("b", "192.0.2.2:443", 0, nil)
	g := raceGroups(t, a, b)["fallback"]
	c, err := g.DialContext(context.Background(), dst)
	require.NoError(t, err)
	_ = c.Close()
	require.Equal(t, "b", g.Unwrap(dst, false).Name())
	a.setErr(nil)
	iface.FlushCache()
	require.Equal(t, "a", g.Unwrap(dst, false).Name(), "a network change kept the node aside")
}

// A finished health check decides for the nodes set aside before it began.
func TestHealthCheckVerdictSettlesSetAside(t *testing.T) {
	var d downNodes
	p := adapter.NewProxy(outbound.NewDirect())
	d.set(p)
	require.True(t, d.has(p))
	d.settled(time.Now().Add(-time.Hour)) // a check that began before the mark
	require.True(t, d.has(p))
	d.settled(time.Now())
	require.False(t, d.has(p))
}
