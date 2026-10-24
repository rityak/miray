package sing_tun

import (
	"errors"
	"testing"

	C "github.com/metacubex/mihomo/constant"

	tun "github.com/metacubex/sing-tun"
)

func TestDefaultTunMTU(t *testing.T) {
	for _, c := range []struct {
		stack C.TUNStack
		goos  string
		want  uint32
	}{
		{C.TunSystem, "linux", 65535},
		{C.TunMips, "linux", 9000},
		{C.TunGvisor, "linux", 65535},
		{C.TunMixed, "linux", 65535},
		{C.TunGvisor, "android", 9000},
		{C.TunSystem, "android", 9000},
		{C.TunSystem, "windows", 9000},
	} {
		if got := defaultTunMTU(c.stack, c.goos); got != c.want {
			t.Errorf("%s/%s: %d, want %d", c.goos, c.stack, got, c.want)
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
