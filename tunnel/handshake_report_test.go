package tunnel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// reportingConn is an inbound with a deferred response (hysteria2's serverConn): it
// records which outcome of the dial it was told.
type reportingConn struct {
	net.Conn
	success chan struct{}
	failure chan error
}

func (c *reportingConn) HandshakeSuccess() error { close(c.success); return nil }
func (c *reportingConn) HandshakeFailure(err error) error {
	c.failure <- err
	return nil
}

type failProxy struct{ handshakeProxy }

func (p *failProxy) DialContext(context.Context, *C.Metadata) (C.Conn, error) {
	return nil, errors.New("refused")
}

// T-102: after the dial the inbound learns the outcome at once, before either side has
// sent a byte: the official hysteria client waits for the response before its data.
func TestHandshakeReported(t *testing.T) {
	oldProxies, oldMode := proxies, mode.Load()
	defer func() { proxies = oldProxies; mode.Store(oldMode) }()
	mode.Store(Direct)
	OnRunning()
	dst := netip.MustParseAddrPort("192.0.2.23:80")
	serverFirst.Set(net.JoinHostPort(dst.Addr().String(), "80"), struct{}{}) // no early-data wait
	run := func(proxy C.Proxy) *reportingConn {
		proxies = map[string]C.Proxy{"DIRECT": proxy}
		client, inbound := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		c := &reportingConn{Conn: inbound, success: make(chan struct{}), failure: make(chan error, 1)}
		metadata := &C.Metadata{NetWork: C.TCP, Type: C.HYSTERIA2, SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 40001, DstIP: dst.Addr(), DstPort: dst.Port()}
		go Tunnel.HandleTCPConn(c, metadata)
		return c
	}

	ok := &handshakeProxy{conns: make(chan *handshakeConn, 1)}
	c := run(ok)
	<-ok.conns
	select {
	case <-c.success:
	case <-time.After(2 * time.Second):
		t.Fatal("handshake success not reported after the dial")
	}

	c = run(&failProxy{})
	select {
	case err := <-c.failure:
		if err == nil {
			t.Fatal("failure reported without the dial error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handshake failure not reported")
	}
}
