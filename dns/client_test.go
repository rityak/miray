package dns

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/ca"

	D "github.com/miekg/dns"
)

// tcp:// queries reuse one connection, and a connection the server closed
// while idle is replaced transparently (T-028).
func TestTCPClientReusesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	closeIdle := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func(conn net.Conn) {
				defer conn.Close()
				dConn := &D.Conn{Conn: conn}
				for {
					query, err := dConn.ReadMsg()
					if err != nil {
						return
					}
					reply := new(D.Msg)
					reply.SetReply(query)
					reply.Answer = []D.RR{&D.A{Hdr: D.RR_Header{Name: query.Question[0].Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, 9)}}
					if err = dConn.WriteMsg(reply); err != nil {
						return
					}
					select {
					case <-closeIdle: // drop the connection after answering
						return
					default:
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })

	client := transform([]NameServer{{Net: "tcp", Addr: listener.Addr().String()}}, nil)[0]
	query := func() {
		t.Helper()
		m := new(D.Msg)
		m.SetQuestion("reuse.example.", D.TypeA)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		reply, err := client.ExchangeContext(ctx, m)
		if err != nil || len(reply.Answer) != 1 {
			t.Fatalf("exchange: %v %v", reply, err)
		}
	}

	for i := 0; i < 5; i++ {
		query()
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("5 queries opened %d connections, want 1", got)
	}

	closeIdle <- struct{}{}
	query()                            // answered, then the server closes the connection
	time.Sleep(100 * time.Millisecond) // let the close reach the client
	query()                            // the stale idle connection fails, a fresh one is used
	if got := accepted.Load(); got != 2 {
		t.Fatalf("connections after the server closed one = %d, want 2", got)
	}
}

// silentServer answers the first query of each connection and then never again,
// as a server or NAT that forgot the connection without a word.
func silentServer(t *testing.T, wrap func(net.Conn) net.Conn) (string, *atomic.Int32) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			if wrap != nil {
				conn = wrap(conn)
			}
			go func(conn net.Conn) {
				defer conn.Close()
				dConn := &D.Conn{Conn: conn}
				for answered := false; ; answered = true {
					query, err := dConn.ReadMsg()
					if err != nil {
						return
					}
					if answered {
						continue
					}
					reply := new(D.Msg)
					reply.SetReply(query)
					reply.Answer = []D.RR{&D.A{Hdr: D.RR_Header{Name: query.Question[0].Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, 9)}}
					if err = dConn.WriteMsg(reply); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return listener.Addr().String(), &accepted
}

func tcpQuery(t *testing.T, c dnsClient) time.Duration {
	t.Helper()
	m := new(D.Msg)
	m.SetQuestion("forgotten.example.", D.TypeA)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // resolver.DefaultDNSTimeout
	defer cancel()
	start := time.Now()
	reply, err := c.ExchangeContext(ctx, m)
	if err != nil || len(reply.Answer) != 1 {
		t.Fatalf("exchange after %v: %v %v", time.Since(start), reply, err)
	}
	return time.Since(start)
}

// A pooled connection the server forgot silently costs the query
// reusedAttemptTimeout, then a fresh one answers: before, the whole timeout went
// to it and the query failed (RFC 7766 §6.2.3, T-156).
func TestTCPClientForgottenConnection(t *testing.T) {
	addr, accepted := silentServer(t, nil)
	c := newClient(addr, nil, "tcp", nil, nil, "")
	tcpQuery(t, c)
	if took := tcpQuery(t, c); took < reusedAttemptTimeout || took > reusedAttemptTimeout+time.Second {
		t.Fatalf("answered after %v, want just over %v", took, reusedAttemptTimeout)
	}
	if got := accepted.Load(); got != 2 {
		t.Fatalf("%d connections, want 2", got)
	}
}

// A connection idle in the pool longer than tcpIdleTimeout is not reused (T-156).
func TestTCPClientIdleLimit(t *testing.T) {
	addr, accepted := silentServer(t, nil)
	c := newClient(addr, nil, "tcp", nil, nil, "")
	tcpQuery(t, c)
	idle := <-c.idle
	idle.since = time.Now().Add(-tcpIdleTimeout - time.Second)
	c.idle <- idle
	if took := tcpQuery(t, c); took >= reusedAttemptTimeout {
		t.Fatalf("answered after %v: the old connection was tried first", took)
	}
	if got := accepted.Load(); got != 2 {
		t.Fatalf("%d connections, want 2", got)
	}
}

// The same for DoT: a forgotten pooled connection costs reusedAttemptTimeout, an
// old one is not tried at all (T-156).
func TestDoTForgottenAndIdleConnections(t *testing.T) {
	certPEM, keyPEM, _, err := ca.NewRandomTLSKeyPair(ca.KeyPairTypeP256)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	addr, accepted := silentServer(t, func(c net.Conn) net.Conn { return tls.Server(c, config) })
	c := newDoTClient(addr, nil, map[string]string{"skip-cert-verify": "true"}, nil, "")
	tcpQuery(t, c)
	if took := tcpQuery(t, c); took < reusedAttemptTimeout || took > reusedAttemptTimeout+time.Second {
		t.Fatalf("answered after %v, want just over %v", took, reusedAttemptTimeout)
	}
	c.access.Lock()
	idle := c.connections.PopBack()
	idle.since = time.Now().Add(-tcpIdleTimeout - time.Second)
	c.connections.PushBack(idle)
	c.access.Unlock()
	if took := tcpQuery(t, c); took >= reusedAttemptTimeout {
		t.Fatalf("answered after %v: the old connection was tried first", took)
	}
	if got := accepted.Load(); got != 3 {
		t.Fatalf("%d connections, want 3", got)
	}
}
