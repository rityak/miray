package sing_tun

import (
	"errors"
	"testing"

	tun "github.com/metacubex/sing-tun"
)

func TestDefaultTunMTU(t *testing.T) {
	for goos, want := range map[string]uint32{"linux": 65535, "android": 9000, "windows": 9000, "darwin": 9000} {
		if got := defaultTunMTU(goos); got != want {
			t.Errorf("%s: %d, want %d", goos, got, want)
		}
	}
}

// An environment that rejects the large default MTU still gets a TUN, at 9000; an MTU
// from the config is never changed behind the user's back.
func TestOpenTunFallsBackFromDefaultMTU(t *testing.T) {
	var tried []tun.Options
	open := func(o tun.Options) (tun.Tun, error) {
		tried = append(tried, o)
		if o.MTU > 9000 {
			return nil, errors.New("invalid argument")
		}
		return nil, nil
	}

	_, got, err := openTun(tun.Options{MTU: 65535, GSO: true}, true, open)
	if err != nil || got.MTU != 9000 || len(tried) != 3 {
		t.Fatalf("default mtu: err %v, mtu %d after %d tries", err, got.MTU, len(tried))
	}

	tried = nil
	_, got, err = openTun(tun.Options{MTU: 65535, GSO: true}, false, open)
	if err == nil || got.MTU != 65535 || len(tried) != 2 {
		t.Fatalf("explicit mtu: err %v, mtu %d after %d tries", err, got.MTU, len(tried))
	}
}
