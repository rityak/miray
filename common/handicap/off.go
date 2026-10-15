//go:build !benchhandicap

// Package handicap injects controlled costs into the data path so a benchmark
// stand can be checked against builds whose loser is known in advance
// (docs/methodology/benchmarking.md §8.3, T-041). Without the benchhandicap
// build tag every hook is an empty inlined function.
package handicap

import "net"

const Enabled = false

// Packet charges the configured per-packet cost; true means drop the packet.
func Packet([]byte) bool { return false }

// WrapConn wraps a relayed TCP connection with the configured stream handicap.
func WrapConn(c net.Conn) net.Conn { return c }
