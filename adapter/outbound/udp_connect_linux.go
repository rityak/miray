//go:build linux

package outbound

import (
	"net"
	"sync"

	"github.com/metacubex/mihomo/common/net/packet"

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
	return &connectedUDPConn{UDPConn: udp, reader: packet.NewEnhancePacketConn(udp)}
}

// connectedUDPConn sends every datagram to the connected server. While corked
// (T-059) it keeps copies of the datagrams instead, and the session's sender
// sends them together with one sendmmsg or GSO write.
type connectedUDPConn struct {
	*net.UDPConn
	reader packet.EnhancePacketConn // replies already queued are read with one recvmmsg (T-075)
	mu     sync.Mutex
	corked bool
	queue  [][]byte // reused across batches
	n      int
}

func (c *connectedUDPConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	if c.corked {
		if c.n == len(c.queue) {
			c.queue = append(c.queue, nil)
		}
		c.queue[c.n] = append(c.queue[c.n][:0], b...)
		c.n++
		c.mu.Unlock()
		return len(b), nil
	}
	c.mu.Unlock()
	return c.UDPConn.Write(b)
}

func (c *connectedUDPConn) ReadFrom(p []byte) (int, net.Addr, error) { return c.reader.ReadFrom(p) }

// Buffered reports the replies already read ahead by recvmmsg, so tunnel can send
// the replies of a drained batch to the local client in one go (T-095).
func (c *connectedUDPConn) Buffered() int {
	if b, ok := c.reader.(interface{ Buffered() int }); ok {
		return b.Buffered()
	}
	return 0
}

// Cork starts collecting the datagrams written.
func (c *connectedUDPConn) Cork() {
	c.mu.Lock()
	c.corked, c.n = true, 0
	c.mu.Unlock()
}

// Uncork stops collecting and returns the datagrams collected, valid until the
// next Cork; the caller sends them on RawUDP.
func (c *connectedUDPConn) Uncork() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.corked = false
	return c.queue[:c.n]
}

func (c *connectedUDPConn) RawUDP() *net.UDPConn { return c.UDPConn }
