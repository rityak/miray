package packet

import (
	"net"
	"sync/atomic"

	"github.com/metacubex/sing/common/buf"
	"github.com/metacubex/sing/common/bufio"
	M "github.com/metacubex/sing/common/metadata"
	N "github.com/metacubex/sing/common/network"
)

type SingPacketConn = N.NetPacketConn

type EnhanceSingPacketConn interface {
	SingPacketConn
	EnhancePacketConn
}

type enhanceSingPacketConn struct {
	SingPacketConn
	packetReadWaiter N.PacketReadWaiter
	lastAddr         atomic.Pointer[cachedUDPAddr]
}

// cachedUDPAddr is the *net.UDPAddr last made for a source; it is never mutated (T-096).
type cachedUDPAddr struct {
	dest M.Socksaddr
	addr *net.UDPAddr
}

func (c *enhanceSingPacketConn) WaitReadFrom() (data []byte, put func(), addr net.Addr, err error) {
	var buff *buf.Buffer
	var dest M.Socksaddr
	rwOptions := N.ReadWaitOptions{}
	if c.packetReadWaiter != nil {
		c.packetReadWaiter.InitializeReadWaiter(rwOptions)
		buff, dest, err = c.packetReadWaiter.WaitReadPacket()
	} else {
		buff = rwOptions.NewPacketBuffer()
		dest, err = c.SingPacketConn.ReadPacket(buff)
		if buff != nil {
			rwOptions.PostReturn(buff)
		}
	}
	if dest.IsFqdn() {
		addr = dest
	} else if last := c.lastAddr.Load(); last != nil && last.dest == dest {
		addr = last.addr // a session's datagrams nearly always come from one source (T-096)
	} else {
		a := dest.UDPAddr()
		c.lastAddr.Store(&cachedUDPAddr{dest: dest, addr: a})
		addr = a
	}
	if err != nil {
		buff.Release()
		return
	}
	if buff == nil {
		return
	}
	if buff.IsEmpty() {
		buff.Release()
		return
	}
	data = buff.Bytes()
	put = buff.Release
	return
}

func (c *enhanceSingPacketConn) Upstream() any {
	return c.SingPacketConn
}

func (c *enhanceSingPacketConn) WriterReplaceable() bool {
	return true
}

func (c *enhanceSingPacketConn) ReaderReplaceable() bool {
	return true
}

func newEnhanceSingPacketConn(conn SingPacketConn) *enhanceSingPacketConn {
	epc := &enhanceSingPacketConn{SingPacketConn: conn}
	if readWaiter, isReadWaiter := bufio.CreatePacketReadWaiter(conn); isReadWaiter {
		epc.packetReadWaiter = readWaiter
	}
	return epc
}
