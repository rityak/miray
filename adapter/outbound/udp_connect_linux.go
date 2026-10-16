//go:build linux

package outbound

import (
	"net"

	"golang.org/x/sys/unix"
)

// connectUDP connects a plain UDP socket to the proxy server (T-061): every
// datagram of the session goes there, and a send on a connected socket is a
// write without an address, for which the kernel reuses the cached route
// instead of looking it up per datagram (sing-box does the same). The socket
// keeps the options the dialer set (interface, routing mark). Anything other
// than a *net.UDPConn (a dialer-proxy chain, for one) is returned unchanged.
func connectUDP(pc net.PacketConn, addr net.Addr) net.PacketConn {
	udp, ok := pc.(*net.UDPConn)
	server, ok2 := addr.(*net.UDPAddr)
	if !ok || !ok2 {
		return pc
	}
	var sa unix.Sockaddr
	if la, ok := udp.LocalAddr().(*net.UDPAddr); ok && la.IP.To4() != nil {
		ip4 := server.IP.To4()
		if ip4 == nil {
			return pc
		}
		s := &unix.SockaddrInet4{Port: server.Port}
		copy(s.Addr[:], ip4)
		sa = s
	} else {
		s := &unix.SockaddrInet6{Port: server.Port}
		copy(s.Addr[:], server.IP.To16()) // an IPv4 server as ::ffff:a.b.c.d on a dual-stack socket
		sa = s
	}
	rc, err := udp.SyscallConn()
	if err != nil {
		return pc
	}
	var connectErr error
	if err := rc.Control(func(fd uintptr) { connectErr = unix.Connect(int(fd), sa) }); err != nil || connectErr != nil {
		return pc
	}
	return &connectedUDPConn{UDPConn: udp}
}

// connectedUDPConn sends every datagram to the connected server.
type connectedUDPConn struct {
	*net.UDPConn
}

func (c *connectedUDPConn) WriteTo(b []byte, _ net.Addr) (int, error) { return c.UDPConn.Write(b) }
