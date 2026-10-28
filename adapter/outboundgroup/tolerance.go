package outboundgroup

import "github.com/metacubex/mihomo/common/atomic"

// relativeTolerance is miray.proxy-groups.relative-tolerance, percent (T-149):
// url-test keeps its node until it is slower than the fastest one by more than
// max(tolerance, fastest × percent / 100), so the margin grows with the delay
// instead of staying a fixed number of milliseconds, as route selection in
// Valve GameNetworkingSockets does (S-020). 0 is mihomo: tolerance alone.
var relativeTolerance = atomic.NewInt32(10)

// SetRelativeTolerance is miray.proxy-groups.relative-tolerance.
func SetRelativeTolerance(percent int) { relativeTolerance.Store(int32(percent)) }

func GetRelativeTolerance() int { return int(relativeTolerance.Load()) }

// slowerThanTolerated reports whether the current node's delay is past the
// margin over the fastest node's, so url-test moves to the fastest.
func slowerThanTolerated(current, fastest, tolerance uint16) bool {
	margin := int(tolerance)
	if relative := int(fastest) * GetRelativeTolerance() / 100; relative > margin {
		margin = relative
	}
	return int(current) > int(fastest)+margin
}
