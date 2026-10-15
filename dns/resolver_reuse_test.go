package dns

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewResolverTakesOverPrevious(t *testing.T) {
	config := func() Config {
		return Config{
			Main:        []NameServer{{Net: "tls", Addr: "1.1.1.1:853"}, {Net: "tcp", Addr: "1.0.0.1:53"}},
			ProxyServer: []NameServer{{Net: "https", Addr: "https://8.8.8.8/dns-query"}},
			Default:     []NameServer{{Net: "udp", Addr: "8.8.4.4:53"}},
		}
	}
	old := NewResolver(config())

	cfg := config()
	cfg.Previous = old
	rs := NewResolver(cfg)
	for i := range old.main {
		require.Same(t, old.main[i], rs.main[i], "main client %d", i)
	}
	require.Same(t, old.ProxyResolver.main[0], rs.ProxyResolver.main[0])
	require.Same(t, old.defaultResolver, rs.defaultResolver)
	require.Same(t, old.cache, rs.cache)
	require.Same(t, old.ProxyResolver.cache, rs.ProxyResolver.cache)

	fresh := NewResolver(config())
	require.NotSame(t, old.main[0], fresh.main[0])
	require.NotSame(t, old.cache, fresh.cache)
}
