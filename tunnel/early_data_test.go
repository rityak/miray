package tunnel

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"
)

// handshakeConn is an outbound connection with a deferred handshake: the first
// Write carries it (VMess/VLESS style). It records when that happens.
type handshakeConn struct {
	N.ExtendedConn
	once  sync.Once
	wrote chan struct{}
}

func (c *handshakeConn) NeedHandshake() bool {
	select {
	case <-c.wrote:
		return false
	default:
		return true
	}
}

func (c *handshakeConn) Write(b []byte) (int, error) {
	c.once.Do(func() { close(c.wrote) })
	return len(b), nil
}

func (c *handshakeConn) Chains() C.Chain               { return C.Chain{"DIRECT"} }
func (c *handshakeConn) ProviderChains() C.Chain       { return nil }
func (c *handshakeConn) AppendToChains(C.ProxyAdapter) {}
func (c *handshakeConn) RemoteDestination() string     { return "" }

type handshakeProxy struct {
	C.Proxy
	conns chan *handshakeConn
}

func (p *handshakeProxy) Name() string { return "DIRECT" }

func (p *handshakeProxy) DialContext(context.Context, *C.Metadata) (C.Conn, error) {
	remote, _ := net.Pipe()
	c := &handshakeConn{ExtendedConn: N.NewExtendedConn(remote), wrote: make(chan struct{})}
	p.conns <- c
	return c, nil
}

// A server-first destination (the client sends nothing) costs the early-data
// wait once; later connections to it skip the wait (T-019).
func TestEarlyDataWaitSkippedForServerFirst(t *testing.T) {
	proxy := &handshakeProxy{conns: make(chan *handshakeConn, 2)}
	oldProxies, oldMode := proxies, mode
	proxies, mode = map[string]C.Proxy{"DIRECT": proxy}, Direct
	defer func() { proxies, mode = oldProxies, oldMode }()
	OnRunning()

	dst := netip.MustParseAddrPort("192.0.2.22:22")
	serverFirst.Delete(net.JoinHostPort(dst.Addr().String(), "22"))
	handshakeAfter := func() time.Duration {
		t.Helper()
		client, inbound := net.Pipe()
		defer client.Close()
		metadata := &C.Metadata{NetWork: C.TCP, Type: C.TUN, SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 40000, DstIP: dst.Addr(), DstPort: dst.Port()}
		start := time.Now()
		done := make(chan struct{})
		go func() {
			Tunnel.HandleTCPConn(inbound, metadata)
			close(done)
		}()
		conn := <-proxy.conns
		select {
		case <-conn.wrote:
		case <-time.After(2 * time.Second):
			t.Fatal("handshake never written")
		}
		elapsed := time.Since(start)
		_ = client.Close()
		select { // finish before the test restores the globals it reads
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("connection handling did not finish")
		}
		return elapsed
	}

	if first := handshakeAfter(); first < earlyDataWait {
		t.Fatalf("first connection handshake after %v, want the %v wait", first, earlyDataWait)
	}
	if second := handshakeAfter(); second > earlyDataWait/2 {
		t.Fatalf("second connection handshake after %v, want no wait", second)
	}
}
