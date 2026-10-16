package tunnel

import (
	"context"
	"fmt"
	"net"

	"github.com/metacubex/mihomo/adapter/inbound"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/common/pool"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/socks5"
)

// udpReadBatch bounds the datagrams taken per recvmmsg (T-060).
const udpReadBatch = 16

type PacketConn struct {
	conn   net.PacketConn
	addr   string
	target socks5.Addr
	proxy  string
	closed bool
}

// RawAddress implements C.Listener
func (l *PacketConn) RawAddress() string {
	return l.addr
}

// Address implements C.Listener
func (l *PacketConn) Address() string {
	return l.conn.LocalAddr().String()
}

// Close implements C.Listener
func (l *PacketConn) Close() error {
	l.closed = true
	return l.conn.Close()
}

func NewUDP(addr, target, proxy string, lc C.InboundListenConfig, tunnel C.Tunnel, additions ...inbound.Addition) (*PacketConn, error) {
	targetAddr := socks5.ParseAddr(target)
	if targetAddr == nil {
		return nil, fmt.Errorf("invalid target address %s", target)
	}
	l, err := lc.ListenPacket(context.Background(), "udp", addr)
	if err != nil {
		return nil, err
	}

	sl := &PacketConn{
		conn:   l,
		target: targetAddr,
		proxy:  proxy,
		addr:   addr,
	}

	if proxy != "" {
		additions = append([]inbound.Addition{inbound.WithSpecialProxy(proxy)}, additions...)
	}

	go func() {
		// the datagrams already queued are read with one recvmmsg (T-060)
		reader := N.NewBatchReader(l, udpReadBatch)
		bufs := make([][]byte, udpReadBatch)
		defer func() {
			for _, buf := range bufs {
				if buf != nil {
					pool.Put(buf)
				}
			}
		}()
		for {
			for i := range bufs {
				if bufs[i] == nil {
					bufs[i] = pool.Get(pool.UDPBufferSize)
				}
			}
			n, err := reader.Read(bufs)
			if err != nil {
				if sl.closed {
					break
				}
				continue
			}
			for i := 0; i < n; i++ {
				sl.handleUDP(l, tunnel, bufs[i][:reader.Len(i)], reader.Addr(i), additions...)
				bufs[i] = nil // handed over with the packet
			}
		}
	}()

	return sl, nil
}

func (l *PacketConn) handleUDP(pc net.PacketConn, tunnel C.Tunnel, buf []byte, addr net.Addr, additions ...inbound.Addition) {
	cPacket := &packet{
		pc:      pc,
		rAddr:   addr,
		payload: buf,
	}

	tunnel.HandleUDPPacket(inbound.NewPacket(l.target, cPacket, C.TUNNEL, additions...))
}
