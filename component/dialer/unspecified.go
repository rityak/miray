package dialer

import (
	"errors"
	"net/netip"

	"github.com/metacubex/mihomo/common/atomic"
)

// ErrUnspecifiedDestination is a destination of 0.0.0.0 or :: (T-146): the
// answer a blocking DNS server gives, never a host to reach (RFC 1122
// §3.2.1.3, RFC 4291 §2.5.2). It is refused at once and not retried; Linux
// would connect it to the local host.
var ErrUnspecifiedDestination = errors.New("unspecified destination address (0.0.0.0 or ::): blocked by DNS, not dialed")

var unspecifiedAsBlock = atomic.NewBool(true)

// SetUnspecifiedAsBlock is miray.dns.unspecified-as-block.
func SetUnspecifiedAsBlock(block bool) { unspecifiedAsBlock.Store(block) }

func GetUnspecifiedAsBlock() bool { return unspecifiedAsBlock.Load() }

// IsBlockedDestination reports a destination refused by unspecified-as-block.
func IsBlockedDestination(ip netip.Addr) bool {
	return ip.IsUnspecified() && unspecifiedAsBlock.Load()
}

// dropUnspecified removes the unspecified addresses from ips; none left is
// ErrUnspecifiedDestination.
func dropUnspecified(ips []netip.Addr) ([]netip.Addr, error) {
	if !unspecifiedAsBlock.Load() {
		return ips, nil
	}
	kept := ips[:0:0]
	for _, ip := range ips {
		if !ip.IsUnspecified() {
			kept = append(kept, ip)
		}
	}
	if len(kept) == len(ips) {
		return ips, nil
	}
	if len(kept) == 0 {
		return nil, ErrUnspecifiedDestination
	}
	return kept, nil
}
