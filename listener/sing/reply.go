package sing

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
)

// ReplyConn is the UDP socket of a listener whose replies tunnel can hold back
// while a session drains the datagrams already queued on its outbound socket,
// then send in one batch (T-068). Sessions share the socket, so the batch goes
// out when the last of them stops holding; the flush runs under the lock, which
// keeps the reused queue away from concurrent writers.
type ReplyConn struct {
	*net.UDPConn
	mu    sync.Mutex
	hold  int
	flush func([][]byte, []*net.UDPAddr) error
	datas [][]byte // reused across batches
	addrs []*net.UDPAddr
	n     int
}

var _ network.NetPacketConn = (*ReplyConn)(nil)

func NewReplyConn(udp *net.UDPConn) *ReplyConn { return &ReplyConn{UDPConn: udp} }

func (c *ReplyConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	n, addr, err := c.ReadFromUDPAddrPort(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	return M.SocksaddrFromNetIP(addr).Unwrap(), nil
}

func (c *ReplyConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	return c.WriteReply(buffer.Bytes(), destination.AddrPort())
}

// WriteReply sends b to dst, or collects a copy of it while a session holds.
func (c *ReplyConn) WriteReply(b []byte, dst netip.AddrPort) error {
	c.mu.Lock()
	if c.hold > 0 {
		if c.n == len(c.datas) {
			c.datas, c.addrs = append(c.datas, nil), append(c.addrs, nil)
		}
		c.datas[c.n] = append(c.datas[c.n][:0], b...)
		// one *net.UDPAddr per run of a client: the batch writer groups GSO runs by pointer
		if c.n > 0 && c.addrs[c.n-1].AddrPort() == dst {
			c.addrs[c.n] = c.addrs[c.n-1]
		} else {
			c.addrs[c.n] = net.UDPAddrFromAddrPort(dst)
		}
		c.n++
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	_, err := c.WriteToUDPAddrPort(b, dst)
	return err
}

// InitFlush sets the batch sender once; newFlush gets the raw socket.
func (c *ReplyConn) InitFlush(newFlush func(*net.UDPConn) func([][]byte, []*net.UDPAddr) error) {
	c.mu.Lock()
	if c.flush == nil {
		c.flush = newFlush(c.UDPConn)
	}
	c.mu.Unlock()
}

// Hold starts collecting replies; every Hold needs a Release.
func (c *ReplyConn) Hold() {
	c.mu.Lock()
	c.hold++
	c.mu.Unlock()
}

// Release sends the collected replies once nobody holds.
func (c *ReplyConn) Release() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hold--
	if c.hold > 0 || c.n == 0 {
		return nil
	}
	n := c.n
	c.n = 0
	if c.flush == nil {
		var err error
		for i := 0; i < n; i++ {
			if _, werr := c.WriteToUDP(c.datas[i], c.addrs[i]); werr != nil && err == nil {
				err = werr
			}
		}
		return err
	}
	return c.flush(c.datas[:n], c.addrs[:n])
}

var ctxKeyReplyConn = contextKey("ReplyConn")

// WithReplyConn marks the packets of a listener with the socket replies go out on.
func WithReplyConn(ctx context.Context, c *ReplyConn) context.Context {
	return context.WithValue(ctx, ctxKeyReplyConn, c)
}

func getReplyConn(ctx context.Context) *ReplyConn {
	c, _ := ctx.Value(ctxKeyReplyConn).(*ReplyConn)
	return c
}
