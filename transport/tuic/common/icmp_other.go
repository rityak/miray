//go:build !linux

package common

import "net"

func enableICMPErrors(net.PacketConn) {}
