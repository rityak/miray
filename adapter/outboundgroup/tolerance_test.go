package outboundgroup

import (
	"sync/atomic"
	"testing"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/adapter/provider"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"

	"github.com/stretchr/testify/require"
)

// measured is a node with a set url-test delay.
type measured struct {
	C.Proxy
	delay atomic.Uint32
}

func (m *measured) AliveForTestUrl(string) bool { return true }

func (m *measured) LastDelayForTestUrl(string) uint16 { return uint16(m.delay.Load()) }

func newMeasured(name string, delay uint16) *measured {
	m := &measured{Proxy: adapter.NewProxy(newNode(name, false))}
	m.delay.Store(uint32(delay))
	return m
}

func newURLTest(t *testing.T, tolerance uint16, nodes ...*measured) *URLTest {
	t.Helper()
	proxies := make([]C.Proxy, len(nodes))
	for i, n := range nodes {
		proxies[i] = n
	}
	pd, err := provider.NewCompatibleProvider("pd", proxies, provider.NewHealthCheck(proxies, "", 0, 0, false, nil))
	require.NoError(t, err)
	opt := GroupCommonOption{Name: "g", TestTimeout: 5000, MaxFailedTimes: 5}
	u, err := NewURLTest(opt, URLTestOption{Tolerance: tolerance}, adapter.NewProxy(outbound.NewReject()), []P.ProxyProvider{pd})
	require.NoError(t, err)
	return u
}

// With relative-tolerance the group stays on its node while it is within the
// percentage of the fastest delay and moves past it; 0 is mihomo, where with the
// default tolerance of 0 any faster node takes over (T-149).
func TestURLTestRelativeTolerance(t *testing.T) {
	defer SetRelativeTolerance(GetRelativeTolerance())
	for _, c := range []struct {
		name      string
		percent   int
		tolerance uint16
		faster    uint16 // delay of b; a, the current node, has 100
		want      string
	}{
		{"within 10%", 10, 0, 95, "a"},
		{"at 10%", 10, 0, 91, "a"}, // 100 > 91+9 is false
		{"past 10%", 10, 0, 85, "b"},
		{"tolerance wider", 10, 30, 80, "a"},
		{"tolerance passed", 10, 30, 60, "b"},
		{"off, any faster", 0, 0, 95, "b"},
		{"off, tolerance", 0, 10, 95, "a"}, // a is first in the list: mihomo moved to b here
	} {
		t.Run(c.name, func(t *testing.T) {
			SetRelativeTolerance(c.percent)
			a, b := newMeasured("a", 100), newMeasured("b", 200)
			u := newURLTest(t, c.tolerance, a, b)
			require.Equal(t, "a", u.Now())
			b.delay.Store(uint32(c.faster))
			u.fastSingle.Reset()
			require.Equal(t, c.want, u.Now())
		})
	}
}

// Delays near the uint16 limit (a dead node reads 65535) do not wrap the margin.
func TestSlowerThanToleratedNoOverflow(t *testing.T) {
	defer SetRelativeTolerance(GetRelativeTolerance())
	SetRelativeTolerance(10)
	require.False(t, slowerThanTolerated(65535, 65000, 0))
	require.True(t, slowerThanTolerated(65535, 100, 65000))
	require.False(t, slowerThanTolerated(65535, 1000, 65000))
}
