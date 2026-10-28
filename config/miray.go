package config

// The miray: block (D-015) holds the options miray has and mihomo does not,
// grouped by subsystem like the rest of the config; it lives in its own file
// so the upstream ones change by a line each.

// RawMiray is miray: in the config file.
type RawMiray struct {
	DNS         RawMirayDNS         `yaml:"dns" json:"dns"`
	ProxyGroups RawMirayProxyGroups `yaml:"proxy-groups" json:"proxy-groups"`
}

// RawMirayDNS is miray.dns.
type RawMirayDNS struct {
	// UnspecifiedAsBlock: an answer of 0.0.0.0 or :: (how DNS blockers answer) is
	// a block: the destination is refused at once, without retries, and never
	// dialed, which on Linux would reach the local host (T-146).
	UnspecifiedAsBlock bool `yaml:"unspecified-as-block" json:"unspecified-as-block"`
}

// RawMirayProxyGroups is miray.proxy-groups.
type RawMirayProxyGroups struct {
	// RelativeTolerance, percent: url-test keeps its node until it is slower than
	// the fastest one by more than max(tolerance, fastest × percent / 100);
	// 0 is tolerance alone, as in mihomo (T-149).
	RelativeTolerance uint16 `yaml:"relative-tolerance" json:"relative-tolerance"`
}

// Miray is the parsed miray: block, part of General.
type Miray struct {
	DNS         MirayDNS         `json:"dns"`
	ProxyGroups MirayProxyGroups `json:"proxy-groups"`
}

type MirayDNS struct {
	UnspecifiedAsBlock bool `json:"unspecified-as-block"`
}

type MirayProxyGroups struct {
	RelativeTolerance int `json:"relative-tolerance"`
}

func defaultRawMiray() RawMiray {
	return RawMiray{
		DNS:         RawMirayDNS{UnspecifiedAsBlock: true},
		ProxyGroups: RawMirayProxyGroups{RelativeTolerance: 10},
	}
}

func parseMiray(cfg *RawConfig) Miray {
	return Miray{
		DNS:         MirayDNS{UnspecifiedAsBlock: cfg.Miray.DNS.UnspecifiedAsBlock},
		ProxyGroups: MirayProxyGroups{RelativeTolerance: int(cfg.Miray.ProxyGroups.RelativeTolerance)},
	}
}
