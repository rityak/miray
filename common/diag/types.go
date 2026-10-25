package diag

import (
	"net/netip"
	"time"
)

// Dial describes a proxied connection: where it came from, where it goes, which rule
// and proxy took it. Host, IP and Port are the destination; the journal keeps only
// the registrable domain and a hash of the IP.
type Dial struct {
	Network, Inbound, Proxy, Rule string
	Via                           string // the group the rule chose when Proxy is its current node
	Host                          string
	IP                            netip.Addr
	Port                          uint16
}

// RelayResult is how both directions of a relayed TCP connection ended. Up is
// client to proxy. A half close lost means a direction finished (FIN) while the
// connection on the other side had no CloseWrite, so it was closed whole and the
// opposite direction was cut (RFC 9293 §3.6). Remote means the error came from the
// proxy side, not from the client's socket.
type RelayResult struct {
	UpErr, DownErr           error
	UpAt, DownAt             time.Time
	UpRemote, DownRemote     bool
	DownFirst                bool  // the download direction finished first
	FirstEnd                 int64 // order of the first direction's end (diag.order)
	UpHalfLost, DownHalfLost bool
}

type TCPEnd struct {
	Dial
	Chain    []string
	ID       string
	Start    time.Time
	Up, Down int64
	State    *ConnState
	Relay    RelayResult
}

type UDPEnd struct {
	Dial
	Chain    []string
	Key      string
	Start    time.Time
	Up, Down int64
	Idle     bool // closed by the idle timer
	Conn     *UDPState
	Sender   *UDPState
}

// Config is what the journal records of a config, without servers or secrets.
type Config struct {
	Home, Version, Built string // the journal folder goes to Home; diag cannot import constant

	TunEnable bool
	TunStack  string
	DNS       string
	Sniffer   bool
	Proxies   map[string]int // type → count
	Groups    map[string]int
}
