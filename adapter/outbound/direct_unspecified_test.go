package outbound

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/component/dialer"
	C "github.com/metacubex/mihomo/constant"
)

// DIRECT does not open a UDP session to 0.0.0.0 or :: (T-146).
func TestDirectUDPUnspecified(t *testing.T) {
	defer dialer.SetUnspecifiedAsBlock(dialer.GetUnspecifiedAsBlock())
	dialer.SetUnspecifiedAsBlock(true)
	d := NewDirect()
	for _, ip := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
		md := &C.Metadata{NetWork: C.UDP, DstIP: ip, DstPort: 443}
		if _, err := d.ListenPacketContext(context.Background(), md); !errors.Is(err, dialer.ErrUnspecifiedDestination) {
			t.Fatalf("%v: %v, want ErrUnspecifiedDestination", ip, err)
		}
	}
}
