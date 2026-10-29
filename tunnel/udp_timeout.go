package tunnel

import (
	"sync/atomic"
	"time"
)

const (
	// mihomoUDPTimeout is mihomo's timeout for every UDP session, kept by
	// miray.udp.timeout: 0.
	mihomoUDPTimeout = 60 * time.Second
	// shortUDPTimeout ends sessions of one-shot protocols sooner, as sing-box
	// does (constant.ProtocolTimeouts): DNS, NTP, STUN.
	shortUDPTimeout = 10 * time.Second
)

// udpTimeout is miray.udp.timeout (T-154): an idle UDP session ends after it.
var udpTimeout atomic.Int64

func init() { udpTimeout.Store(int64(5 * time.Minute)) }

// SetUDPTimeout is miray.udp.timeout; 0 is mihomo's 60 s for every port.
func SetUDPTimeout(d time.Duration) { udpTimeout.Store(int64(d)) }

// udpSessionTimeout is the idle timeout of a session to port. RFC 4787 REQ-5: a
// NAT mapping lives at least 2 min, 5 recommended; shorter only for well-known
// ports (REQ-5a). STUN's 3478 is not one, but sing-box ends it as soon, and ICE
// consent checks (RFC 7675) refresh a live call every few seconds.
func udpSessionTimeout(port uint16) time.Duration {
	d := time.Duration(udpTimeout.Load())
	if d == 0 {
		return mihomoUDPTimeout
	}
	switch port {
	case 53, 123, 3478:
		if d > shortUDPTimeout {
			return shortUDPTimeout
		}
	}
	return d
}
