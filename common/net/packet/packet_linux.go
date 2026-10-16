//go:build linux

package packet

import (
	"errors"
	"net"
	"sync"
	"syscall"

	"github.com/metacubex/mihomo/common/pool"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// maxReadBatch bounds the datagrams one recvmmsg takes off a socket.
const maxReadBatch = 8

// enhanceUDPConn reads the datagrams already waiting on the socket with one
// recvmmsg and hands them out one per WaitReadFrom (T-051), never waiting for
// more than the first. Buffers are taken only once the socket is readable, so
// an idle session holds none; the batch grows while reads come back full and
// stays at two buffers for a sparse flow.
type enhanceUDPConn struct {
	*net.UDPConn
	rawConn syscall.RawConn

	mu    sync.Mutex
	v4    *ipv4.PacketConn
	v6    *ipv6.PacketConn
	msgs  []ipv4.Message
	size  int // buffers offered to the next recvmmsg
	queue []ipv4.Message
}

func (c *enhanceUDPConn) init() {
	c.rawConn, _ = c.UDPConn.SyscallConn()
	if la, ok := c.UDPConn.LocalAddr().(*net.UDPAddr); ok && la.IP.To4() != nil {
		c.v4 = ipv4.NewPacketConn(c.UDPConn)
	} else {
		c.v6 = ipv6.NewPacketConn(c.UDPConn)
	}
	c.msgs = make([]ipv4.Message, maxReadBatch)
	for i := range c.msgs {
		c.msgs[i].Buffers = make([][]byte, 1)
	}
	c.size = 1
}

func (c *enhanceUDPConn) WaitReadFrom() (data []byte, put func(), addr net.Addr, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.msgs == nil {
		c.init()
	}
	for len(c.queue) == 0 {
		if err = c.readBatch(); err != nil {
			return nil, nil, nil, err
		}
	}
	m := c.queue[0]
	c.queue = c.queue[1:]
	buf := m.Buffers[0]
	return buf[:m.N], func() { _ = pool.Put(buf) }, m.Addr, nil
}

// readBatch fills the queue with at least one datagram, waiting for the socket
// to become readable without holding buffers.
func (c *enhanceUDPConn) readBatch() error {
	for {
		ms := c.msgs[:c.size]
		for i := range ms {
			ms[i].Buffers[0] = pool.Get(pool.UDPBufferSize)
		}
		var n int
		var err error
		if c.v4 != nil {
			n, err = c.v4.ReadBatch(ms, unix.MSG_DONTWAIT)
		} else {
			n, err = c.v6.ReadBatch(ms, unix.MSG_DONTWAIT)
		}
		if n < 0 { // x/net passes on the -1 of a failed recvmmsg (P-016)
			n = 0
		}
		for i := n; i < len(ms); i++ {
			_ = pool.Put(ms[i].Buffers[0])
		}
		if n > 0 {
			c.queue = append(c.queue[:0], ms[:n]...)
			if c.size = 2 * n; c.size > maxReadBatch {
				c.size = maxReadBatch
			}
			return nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return err
		}
		// wait for readability, honouring the read deadline
		waited := false
		if err := c.rawConn.Read(func(uintptr) bool {
			done := waited
			waited = true
			return done
		}); err != nil {
			return err
		}
	}
}

// ReadFrom goes through the same queue, so it never skips datagrams already read.
func (c *enhanceUDPConn) ReadFrom(p []byte) (int, net.Addr, error) {
	data, put, addr, err := c.WaitReadFrom()
	if err != nil {
		return 0, nil, err
	}
	n := copy(p, data)
	put()
	return n, addr, nil
}
