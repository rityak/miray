package config

import "testing"

// miray.dns.unspecified-as-block is on unless the config turns it off (T-146, D-015).
func TestMirayUnspecifiedAsBlock(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want bool
	}{
		{"mode: rule\n", true},
		{"miray: {}\n", true},
		{"miray: {dns: {}}\n", true},
		{"miray: {dns: {unspecified-as-block: false}}\n", false},
		{"miray:\n  dns:\n    unspecified-as-block: true\n", true},
	} {
		raw, err := UnmarshalRawConfig([]byte(c.yaml))
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		general, err := parseGeneral(raw)
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if got := general.Miray.DNS.UnspecifiedAsBlock; got != c.want {
			t.Errorf("%q: unspecified-as-block %v, want %v", c.yaml, got, c.want)
		}
	}
}

func TestMirayRelativeTolerance(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want int
	}{
		{"mode: rule\n", 10},
		{"miray: {}\n", 10},
		{"miray: {proxy-groups: {}}\n", 10},
		{"miray: {proxy-groups: {relative-tolerance: 0}}\n", 0},
		{"miray:\n  proxy-groups:\n    relative-tolerance: 25\n", 25},
	} {
		raw, err := UnmarshalRawConfig([]byte(c.yaml))
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		general, err := parseGeneral(raw)
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if got := general.Miray.ProxyGroups.RelativeTolerance; got != c.want {
			t.Errorf("%q: relative-tolerance %v, want %v", c.yaml, got, c.want)
		}
	}
	if _, err := UnmarshalRawConfig([]byte("miray: {proxy-groups: {relative-tolerance: -5}}\n")); err == nil {
		t.Error("a negative relative-tolerance is accepted")
	}
}

func TestMirayUDPTimeout(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want int
	}{
		{"mode: rule\n", 300},
		{"miray: {}\n", 300},
		{"miray: {udp: {}}\n", 300},
		{"miray: {udp: {timeout: 0}}\n", 0},
		{"miray:\n  udp:\n    timeout: 120\n", 120},
	} {
		raw, err := UnmarshalRawConfig([]byte(c.yaml))
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		general, err := parseGeneral(raw)
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if got := general.Miray.UDP.Timeout; got != c.want {
			t.Errorf("%q: timeout %v, want %v", c.yaml, got, c.want)
		}
	}
	if _, err := UnmarshalRawConfig([]byte("miray: {udp: {timeout: -1}}\n")); err == nil {
		t.Error("a negative timeout is accepted")
	}
}
