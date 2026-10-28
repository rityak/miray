package config

// The miray: block (D-015) holds the options miray has and mihomo does not,
// grouped by subsystem like the rest of the config; it lives in its own file
// so the upstream ones change by a line each.

// RawMiray is miray: in the config file.
type RawMiray struct {
	DNS RawMirayDNS `yaml:"dns" json:"dns"`
}

// RawMirayDNS is miray.dns.
type RawMirayDNS struct {
	// UnspecifiedAsBlock: an answer of 0.0.0.0 or :: (how DNS blockers answer) is
	// a block: the destination is refused at once, without retries, and never
	// dialed, which on Linux would reach the local host (T-146).
	UnspecifiedAsBlock bool `yaml:"unspecified-as-block" json:"unspecified-as-block"`
}

// Miray is the parsed miray: block, part of General.
type Miray struct {
	DNS MirayDNS `json:"dns"`
}

type MirayDNS struct {
	UnspecifiedAsBlock bool `json:"unspecified-as-block"`
}

func defaultRawMiray() RawMiray {
	return RawMiray{DNS: RawMirayDNS{UnspecifiedAsBlock: true}}
}

func parseMiray(cfg *RawConfig) Miray {
	return Miray{DNS: MirayDNS{UnspecifiedAsBlock: cfg.Miray.DNS.UnspecifiedAsBlock}}
}
