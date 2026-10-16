//go:build !linux

package outbound

import "net"

func connectUDP(pc net.PacketConn, _ net.Addr) net.PacketConn { return pc }
