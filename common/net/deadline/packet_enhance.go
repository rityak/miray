package deadline

import (
	"net"
	"os"
	"runtime"

	"github.com/metacubex/mihomo/common/net/packet"
)

type EnhancePacketConn struct {
	*NetPacketConn
	enhancePacketConn
}

var _ packet.EnhancePacketConn = (*EnhancePacketConn)(nil)

func NewEnhancePacketConn(pc packet.EnhancePacketConn) packet.EnhancePacketConn {
	return NewNetPacketConn(pc).(packet.EnhancePacketConn)
}

type enhanceReadResult struct {
	data []byte
	put  func()
	addr net.Addr
	err  error
}

type enhancePacketConn struct {
	netPacketConn     *NetPacketConn
	enhancePacketConn packet.EnhancePacketConn
}

func (c *enhancePacketConn) WaitReadFrom() (data []byte, put func(), addr net.Addr, err error) {
	// No read deadline was ever set, so there is no pipe read or cached result: read directly,
	// without the select and the token round trip below, which a UDP relay paid per datagram
	// (T-096). inRead is set before the second check, so a concurrent SetReadDeadline either
	// is seen here or sees the read in flight and takes the native path, as for the zero deadline.
	if !c.netPacketConn.deadlineUsed.Load() {
		c.netPacketConn.inRead.Store(true)
		if !c.netPacketConn.deadlineUsed.Load() {
			data, put, addr, err = c.enhancePacketConn.WaitReadFrom()
			c.netPacketConn.inRead.Store(false)
			return
		}
		c.netPacketConn.inRead.Store(false)
	}
FOR:
	for {
		select {
		case result := <-c.netPacketConn.resultCh:
			if result != nil {
				if result, ok := result.(*enhanceReadResult); ok {
					data = result.data
					put = result.put
					addr = result.addr
					err = result.err
					c.netPacketConn.resultCh <- nil // finish cache read
					return
				}
				c.netPacketConn.resultCh <- result // another type of read
				runtime.Gosched()                  // allowing other goroutines to run
				continue FOR
			} else {
				c.netPacketConn.resultCh <- nil
				break FOR
			}
		case <-c.netPacketConn.pipeDeadline.Wait():
			return nil, nil, nil, os.ErrDeadlineExceeded
		}
	}

	if c.netPacketConn.disablePipe.Load() {
		return c.enhancePacketConn.WaitReadFrom()
	} else if c.netPacketConn.deadline.Load().IsZero() {
		c.netPacketConn.inRead.Store(true)
		defer c.netPacketConn.inRead.Store(false)
		data, put, addr, err = c.enhancePacketConn.WaitReadFrom()
		return
	}

	<-c.netPacketConn.resultCh
	go c.pipeWaitReadFrom()

	return c.WaitReadFrom()
}

func (c *enhancePacketConn) pipeWaitReadFrom() {
	data, put, addr, err := c.enhancePacketConn.WaitReadFrom()
	result := &enhanceReadResult{}
	result.data = data
	result.put = put
	result.addr = addr
	result.err = err
	c.netPacketConn.resultCh <- result
}
