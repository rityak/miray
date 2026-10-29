//go:build linux

package packet

import (
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"unsafe"

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

	// a connected socket (T-096) reads with its own recvmmsg, without source addresses
	remote net.Addr
	iovs   []unix.Iovec
	hdrs   []mmsghdr
	readFn func(fd uintptr) bool // recvConnected, made once: a closure per read allocated
	rn     int
	rerr   error
}

// mmsghdr is struct mmsghdr of recvmmsg(2); Go pads it as C does on 32 and 64 bits.
type mmsghdr struct {
	hdr unix.Msghdr
	len uint32
}

// NewConnectedEnhancePacketConn is NewEnhancePacketConn for a socket connected to remote:
// every datagram comes from remote, so it is not parsed per datagram (T-096).
func NewConnectedEnhancePacketConn(udp *net.UDPConn, remote net.Addr) EnhancePacketConn {
	return &enhanceUDPConn{UDPConn: udp, remote: remote}
}

func (c *enhanceUDPConn) init() {
	c.rawConn, _ = c.UDPConn.SyscallConn()
	if c.remote != nil {
		c.iovs = make([]unix.Iovec, maxReadBatch)
		c.hdrs = make([]mmsghdr, maxReadBatch)
		c.readFn = c.recvConnected
	}
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

// Buffered returns how many datagrams already read wait in the queue (T-068).
func (c *enhanceUDPConn) Buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

// readBatch fills the queue with at least one datagram, waiting for the socket
// to become readable without holding buffers.
func (c *enhanceUDPConn) readBatch() error {
	if c.remote != nil {
		return c.readConnected()
	}
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

// readConnected is readBatch for a connected socket, done as sing does it
// (sing/common/bufio/packet_batch_mmsg.go): recvmmsg straight from the poller's
// callback, headers kept across calls, no names. Through x/net every call packed
// all slots and parsed and allocated an address per datagram, which at 50 Mbit/s
// cost more than the batch saved (T-096). Buffers are still taken only while the
// socket has data.
func (c *enhanceUDPConn) readConnected() error {
	c.rn, c.rerr = 0, nil
	if err := c.rawConn.Read(c.readFn); err != nil {
		return err
	}
	if c.rerr != nil {
		return c.rerr
	}
	n := c.rn
	for i := 0; i < n; i++ {
		c.msgs[i].N = int(c.hdrs[i].len)
		c.msgs[i].Addr = c.remote
	}
	c.queue = append(c.queue[:0], c.msgs[:n]...)
	if c.size = 2 * n; c.size > maxReadBatch {
		c.size = maxReadBatch
	}
	return nil
}

// recvConnected is the poller callback of readConnected: false waits for readability.
func (c *enhanceUDPConn) recvConnected(fd uintptr) bool {
	ms := c.msgs[:c.size]
	for i := range ms {
		b := pool.Get(pool.UDPBufferSize)
		ms[i].Buffers[0] = b
		c.iovs[i].Base = &b[0]
		c.iovs[i].SetLen(len(b))
		c.hdrs[i] = mmsghdr{}
		c.hdrs[i].hdr.Iov = &c.iovs[i]
		c.hdrs[i].hdr.SetIovlen(1)
	}
	var r uintptr
	var errno syscall.Errno
	for {
		r, _, errno = unix.Syscall6(unix.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&c.hdrs[0])), uintptr(len(ms)), unix.MSG_DONTWAIT, 0, 0)
		if errno != unix.EINTR {
			break
		}
	}
	n := 0
	if errno == 0 {
		n = int(r)
	}
	for i := n; i < len(ms); i++ {
		_ = pool.Put(ms[i].Buffers[0])
	}
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK || (errno == 0 && n == 0) {
		return false // wait for readability, honouring the read deadline
	}
	if errno == unix.ECONNREFUSED {
		// an ICMP unreachable reaches a connected socket only; it must not end the
		// session (RFC 4787 REQ-11), and an unconnected one never sees it (T-154)
		return false
	}
	if errno != 0 {
		c.rerr = os.NewSyscallError("recvmmsg", errno)
	}
	c.rn = n
	return true
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
