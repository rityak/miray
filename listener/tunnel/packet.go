package tunnel

import (
	"net"

	"github.com/metacubex/mihomo/common/pool"
	"github.com/metacubex/mihomo/listener/sing"
)

type packet struct {
	pc      net.PacketConn
	reply   *sing.ReplyConn // nil unless the listener socket is a *net.UDPConn
	rAddr   net.Addr
	payload []byte
}

func (c *packet) Data() []byte {
	return c.payload
}

// WriteBack write UDP packet with source(ip, port) = `addr`
func (c *packet) WriteBack(b []byte, addr net.Addr) (n int, err error) {
	if c.reply != nil {
		if a, ok := c.rAddr.(*net.UDPAddr); ok {
			return len(b), c.reply.WriteReply(b, a.AddrPort())
		}
	}
	return c.pc.WriteTo(b, c.rAddr)
}

// ReplyConn lets tunnel send the replies of a drained batch in one go (T-068, T-095).
func (c *packet) ReplyConn() any {
	if c.reply == nil {
		return nil
	}
	return c.reply
}

// LocalAddr returns the source IP/Port of UDP Packet
func (c *packet) LocalAddr() net.Addr {
	return c.rAddr
}

func (c *packet) Drop() {
	_ = pool.Put(c.payload)
	c.payload = nil
}

func (c *packet) InAddr() net.Addr {
	return c.pc.LocalAddr()
}
