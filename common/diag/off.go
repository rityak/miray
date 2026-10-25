//go:build !miraydiag

// Package diag writes a field diagnostics journal: only outcomes a user would notice
// (a connection that failed, stalled, got no answer, lost data) and departures from
// the standards, as JSON lines in miray-diag/ next to the config (T-138, S-019).
// Without the miraydiag build tag Enabled is false and every hook is an empty
// function: call sites guard their hooks with `if diag.Enabled`, so a production
// build carries none of it, and the hook state types are zero-size.
package diag

import (
	"context"
	"net"
	"time"

	"github.com/metacubex/quic-go"
)

const Enabled = false

// Trace follows the dial of one proxied connection through its context.
type Trace struct{}

func WithTrace(ctx context.Context) (context.Context, *Trace) { return ctx, nil }
func ServerDNSFailed(context.Context)                         {}
func Connected(context.Context, time.Time, error)             {}
func (*Trace) Failed(error)                                   {}

// ConnState is the activity of a relayed TCP connection.
type ConnState struct{}

func (*ConnState) Up(int64)   {}
func (*ConnState) Down(int64) {}
func (*ConnState) Closed()    {}

// UDPState counts the datagrams of a UDP session that never left.
type UDPState struct{}

func (*UDPState) QueueDrop()     {}
func (*UDPState) FullDrop()      {}
func (*UDPState) WriteErr(error) {}
func (*UDPState) SetIdle()       {}
func (*UDPState) End(error)      {}
func (*UDPState) IsIdle() bool   { return false }

func DialFailed(*Trace, Dial, error)                              {}
func DialDone(*Trace, Dial, []string)                             {}
func Dropped(string, Dial)                                        {}
func TCPClosed(TCPEnd)                                            {}
func UDPStarted(string, Dial)                                     {}
func UDPClosed(UDPEnd)                                            {}
func WatchQUIC(string, *quic.Conn, time.Time)                     {}
func DNS(string, string, string, time.Duration, error, int, bool) {}
func GroupPick(string, string)                                    {}
func GroupRace(string, string, string)                            {}
func GroupAside(string, string, string, bool, error)              {}
func GroupCheck(string, string)                                   {}
func GroupChecked(string, int, int)                               {}
func NodeAlive(string, bool)                                      {}
func NetChange(string)                                            {}
func TunError(error)                                              {}
func Configured(Config)                                           {}
func SetLookups(func(string) string, func() (int, int))           {}

// Relay is common/net.Relay that also reports how each direction ended.
func Relay(net.Conn, net.Conn) RelayResult { return RelayResult{} }
