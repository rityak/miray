package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	D "github.com/miekg/dns"
)

type client struct {
	port   string
	host   string
	dialer *dnsDialer
	schema string
	// idle keeps finished tcp:// connections for the next query (RFC 7766
	// connection reuse) instead of a new handshake per query (T-028).
	idle chan idleConn
}

// tcpIdleConnections bounds the reused connections kept per tcp:// server.
const tcpIdleConnections = 4

const (
	// tcpIdleTimeout: a pooled tcp:// or DoT connection idle longer is closed, not
	// reused. Servers close theirs after 10–30 s (RFC 7766 §6.2.3), and one that
	// a server or NAT forgot silently cost the query its whole timeout (T-156).
	tcpIdleTimeout = 10 * time.Second
	// reusedAttemptTimeout bounds a query on a pooled connection: without an
	// answer by then it is sent again on a new connection, within the query's
	// own timeout.
	reusedAttemptTimeout = 2 * time.Second
)

type idleConn struct {
	net.Conn
	since time.Time
}

var _ dnsClient = (*client)(nil)

// Address implements dnsClient
func (c *client) Address() string {
	return fmt.Sprintf("%s://%s", c.schema, net.JoinHostPort(c.host, c.port))
}

func (c *client) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	network := "udp"
	if c.schema != "udp" {
		network = "tcp"
	}
	addr := net.JoinHostPort(c.host, c.port)

	conn, reused := c.takeIdle()
	if conn == nil {
		var err error
		if conn, err = c.dialer.DialContext(ctx, network, addr); err != nil {
			return nil, err
		}
	}
	timeout := resolver.DefaultDNSTimeout
	if reused {
		timeout = reusedAttemptTimeout
	}
	msg, err := c.exchange(ctx, conn, network, addr, m, timeout)
	if err != nil && reused && ctx.Err() == nil {
		// The server may have closed an idle connection, or forgotten it without a
		// word; retry once on a fresh one.
		if conn, err = c.dialer.DialContext(ctx, network, addr); err != nil {
			return nil, err
		}
		msg, err = c.exchange(ctx, conn, network, addr, m, resolver.DefaultDNSTimeout)
	}
	return msg, err
}

// exchange owns conn: it is returned to the idle pool after a successful tcp
// exchange and closed otherwise. Only this goroutine pools it, after the
// result arrived: a cancel racing with the end of the exchange closes conn,
// and a closed conn must not reach the pool.
func (c *client) exchange(ctx context.Context, conn net.Conn, network, addr string, m *D.Msg, timeout time.Duration) (*D.Msg, error) {
	type result struct {
		msg   *D.Msg
		err   error
		reuse bool
	}
	ch := make(chan result, 1)
	go func() {
		dClient := &D.Client{
			UDPSize: 4096,
			Timeout: timeout,
		}
		dConn := &D.Conn{
			Conn:    conn,
			UDPSize: dClient.UDPSize,
		}

		msg, _, err := dClient.ExchangeWithConn(m, dConn)
		reuse := err == nil && network == "tcp"
		if !reuse {
			_ = conn.Close()
		}

		if msg != nil && msg.Truncated && network == "udp" {
			log.Debugln("[DNS] Truncated reply from %s:%s for %s over UDP, retrying over TCP", c.host, c.port, m.Question[0].String())
			var tcpConn net.Conn
			tcpConn, err = c.dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				ch <- result{msg, err, false}
				return
			}
			defer tcpConn.Close()
			dConn.Conn = tcpConn
			msg, _, err = dClient.ExchangeWithConn(m, dConn)
		}

		ch <- result{msg, err, reuse}
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close() // abort the exchange
		return nil, ctx.Err()
	case ret := <-ch:
		if ret.reuse {
			c.putIdle(conn)
		}
		return ret.msg, ret.err
	}
}

func (c *client) takeIdle() (net.Conn, bool) {
	if c.idle == nil {
		return nil, false
	}
	for {
		select {
		case conn := <-c.idle:
			if time.Since(conn.since) > tcpIdleTimeout {
				_ = conn.Close()
				continue
			}
			return conn.Conn, true
		default:
			return nil, false
		}
	}
}

func (c *client) putIdle(conn net.Conn) {
	if c.idle == nil {
		_ = conn.Close()
		return
	}
	select {
	case c.idle <- idleConn{conn, time.Now()}:
	default:
		_ = conn.Close()
	}
}

// ResetConnection closes the idle tcp:// connections, e.g. after a network change.
func (c *client) ResetConnection() {
	for {
		conn, ok := c.takeIdle()
		if !ok {
			return
		}
		_ = conn.Close()
	}
}

func newClient(addr string, resolver resolver.Resolver, netType string, params map[string]string, proxyAdapter C.ProxyAdapter, proxyName string) *client {
	host, port, _ := net.SplitHostPort(addr)
	c := &client{
		port:   port,
		host:   host,
		dialer: newDNSDialer(resolver, proxyAdapter, proxyName),
		schema: "udp",
	}
	if strings.HasPrefix(netType, "tcp") {
		c.schema = "tcp"
		c.idle = make(chan idleConn, tcpIdleConnections)
	}
	return c
}
