package outboundgroup

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/adapter/provider"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"

	"github.com/stretchr/testify/require"
)

// node is a proxy that either fails to reach its server or connects.
type node struct {
	*outbound.Base
	fail  atomic.Bool
	dials atomic.Int32
}

func newNode(name string, fail bool) *node {
	n := &node{Base: outbound.NewBase(outbound.BaseOption{Name: name, Type: C.Socks5, UDP: true})}
	n.fail.Store(fail)
	return n
}

var errNodeDown = errors.New("dial tcp 192.0.2.1:1080: i/o timeout")

func (n *node) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	n.dials.Add(1)
	if n.fail.Load() {
		return nil, errNodeDown
	}
	c, _ := net.Pipe()
	return outbound.NewConn(c, n), nil
}

func (n *node) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	n.dials.Add(1)
	if n.fail.Load() {
		return nil, errNodeDown
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return outbound.NewPacketConn(pc, n), nil
}

func groupProviders(t *testing.T, nodes ...*node) []P.ProxyProvider {
	t.Helper()
	proxies := make([]C.Proxy, len(nodes))
	for i, n := range nodes {
		proxies[i] = adapter.NewProxy(n)
	}
	pd, err := provider.NewCompatibleProvider("pd", proxies, provider.NewHealthCheck(proxies, "", 0, 0, false, nil))
	require.NoError(t, err)
	return []P.ProxyProvider{pd}
}

type testGroup interface {
	C.ProxyAdapter
}

func newGroups(t *testing.T, nodes ...*node) map[string]testGroup {
	t.Helper()
	empty := adapter.NewProxy(outbound.NewReject())
	opt := GroupCommonOption{Name: "g", TestTimeout: 5000, MaxFailedTimes: 5}
	fb, err := NewFallback(opt, FallbackOption{}, empty, groupProviders(t, nodes...))
	require.NoError(t, err)
	ut, err := NewURLTest(opt, URLTestOption{}, empty, groupProviders(t, nodes...))
	require.NoError(t, err)
	lb, err := NewLoadBalance(opt, LoadBalanceOption{Strategy: "round-robin"}, empty, groupProviders(t, nodes...))
	require.NoError(t, err)
	return map[string]testGroup{"fallback": fb, "url-test": ut, "load-balance": lb}
}

var dst = &C.Metadata{NetWork: C.TCP, Host: "example.com", DstPort: 443}

// A node that cannot reach its server does not fail the connection: the same dial goes
// through the next node before any data is sent (RFC 8305), and since the next node got
// through, the first is set aside for new connections at once (T-129).
func TestGroupFailoverToNextNode(t *testing.T) {
	for _, name := range []string{"fallback", "url-test", "load-balance"} {
		t.Run(name, func(t *testing.T) {
			a, b := newNode("a", true), newNode("b", false)
			g := newGroups(t, a, b)[name]
			for i := 0; i < 4; i++ {
				c, err := g.DialContext(context.Background(), dst)
				require.NoError(t, err, "dial %d", i)
				_ = c.Close()
			}
			require.LessOrEqual(t, a.dials.Load(), int32(1), "the failed node is retried for new connections")
			require.EqualValues(t, 4, b.dials.Load())
		})
	}
}

// The same for UDP sessions.
func TestGroupFailoverPacket(t *testing.T) {
	for _, name := range []string{"fallback", "url-test", "load-balance"} {
		t.Run(name, func(t *testing.T) {
			a, b := newNode("a", true), newNode("b", false)
			g := newGroups(t, a, b)[name]
			for i := 0; i < 3; i++ {
				pc, err := g.ListenPacketContext(context.Background(), &C.Metadata{NetWork: C.UDP, Host: "example.com", DstPort: 443})
				require.NoError(t, err, "session %d", i)
				_ = pc.Close()
			}
			require.LessOrEqual(t, a.dials.Load(), int32(1))
		})
	}
}

// When the next node fails too, the target is the likely cause (the site is down, not
// the proxies): the error comes back after two attempts and no node is set aside.
func TestGroupBothNodesFailKeepsNodes(t *testing.T) {
	a, b := newNode("a", true), newNode("b", true)
	g := newGroups(t, a, b)["fallback"]
	_, err := g.DialContext(context.Background(), dst)
	require.ErrorIs(t, err, errNodeDown)
	require.EqualValues(t, 1, a.dials.Load())
	require.EqualValues(t, 1, b.dials.Load())
	b.fail.Store(false)
	a.fail.Store(false)
	_, err = g.DialContext(context.Background(), dst)
	require.NoError(t, err)
	require.EqualValues(t, 2, a.dials.Load(), "a node was set aside without proof")
}

// A canceled dial is not retried, and a single node has nothing to fail over to.
func TestGroupFailoverLimits(t *testing.T) {
	a, b := newNode("a", true), newNode("b", false)
	g := newGroups(t, a, b)["fallback"]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.DialContext(ctx, dst)
	require.Error(t, err)
	require.EqualValues(t, 0, b.dials.Load(), "a canceled dial went on to the next node")

	solo := newNode("solo", true)
	g = newGroups(t, solo)["fallback"]
	_, err = g.DialContext(context.Background(), dst)
	require.ErrorIs(t, err, errNodeDown)
	require.EqualValues(t, 1, solo.dials.Load())
}
