package config

import (
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestTunGSODefault(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		gso   *bool
		stack C.TUNStack
		goos  string
		want  bool
	}{
		{nil, C.TunMips, "linux", true},
		{nil, C.TunSystem, "linux", true},
		{nil, C.TunGvisor, "linux", false},
		{nil, C.TunMixed, "linux", false},
		{nil, C.TunMips, "android", false},
		{nil, C.TunMips, "windows", false},
		{&off, C.TunMips, "linux", false},
		{&on, C.TunGvisor, "windows", true},
	} {
		if got := tunGSO(tc.gso, tc.stack, tc.goos); got != tc.want {
			t.Errorf("tunGSO(%v, %v, %s) = %v, want %v", tc.gso != nil && *tc.gso, tc.stack, tc.goos, got, tc.want)
		}
	}
}
