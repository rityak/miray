package executor

import (
	"github.com/metacubex/mihomo/common/diag"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

// diagConfigured records the config in the diagnostics journal (T-138): kinds of
// proxies and groups, TUN and DNS modes; no servers or secrets.
func diagConfigured(cfg *config.Config) {
	c := diag.Config{
		Home: C.Path.HomeDir(), Version: C.Version, Built: C.BuildTime,
		TunEnable: cfg.General.Tun.Enable, TunStack: cfg.General.Tun.Stack.String(), DNS: "off",
		Proxies: map[string]int{}, Groups: map[string]int{},
	}
	if cfg.DNS != nil && cfg.DNS.Enable {
		c.DNS = cfg.DNS.EnhancedMode.String()
	}
	if cfg.Sniffer != nil {
		c.Sniffer = cfg.Sniffer.Enable
	}
	count := func(p C.Proxy) {
		switch t := p.Type(); t {
		case C.Direct, C.Reject, C.RejectDrop, C.Compatible, C.Pass, C.PassRule, C.Dns:
		case C.Selector, C.Fallback, C.URLTest, C.LoadBalance, C.Relay:
			c.Groups[t.String()]++
		default:
			c.Proxies[t.String()]++
		}
	}
	for _, p := range cfg.Proxies {
		count(p)
	}
	for _, pd := range cfg.Providers {
		if pd.VehicleType() == P.Compatible { // a group's own list: its proxies are counted above
			continue
		}
		for _, p := range pd.Proxies() {
			count(p)
		}
	}
	diag.Configured(c)
}
