package dialer

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
)

// An unspecified address (a DNS blocker's answer) is refused at once and never
// dialed; with miray.dns.unspecified-as-block off it is dialed as before (T-146).
func TestUnspecifiedAsBlock(t *testing.T) {
	defer SetUnspecifiedAsBlock(GetUnspecifiedAsBlock())
	SetUnspecifiedAsBlock(true)
	defer func(v bool) { resolver.DisableIPv6 = v }(resolver.DisableIPv6)
	resolver.DisableIPv6 = false
	for _, address := range []string{"0.0.0.0:443", "[::]:443"} {
		start := time.Now()
		_, err := DialContext(context.Background(), "tcp", address)
		if !errors.Is(err, ErrUnspecifiedDestination) {
			t.Fatalf("%s: %v, want ErrUnspecifiedDestination", address, err)
		}
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Fatalf("%s: refused after %v, want at once", address, d)
		}
	}

	a, b := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::1")
	kept, err := dropUnspecified([]netip.Addr{netip.IPv4Unspecified(), a, netip.IPv6Unspecified(), b})
	if err != nil || len(kept) != 2 || kept[0] != a || kept[1] != b {
		t.Fatalf("mixed answer kept %v, %v; want the two real addresses", kept, err)
	}

	SetUnspecifiedAsBlock(false)
	_, err = DialContext(context.Background(), "tcp", "0.0.0.0:1")
	if errors.Is(err, ErrUnspecifiedDestination) {
		t.Fatal("refused with the option off")
	}
	if IsBlockedDestination(netip.IPv4Unspecified()) {
		t.Fatal("blocked with the option off")
	}
}
