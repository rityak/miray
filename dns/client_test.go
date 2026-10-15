package dns

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

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
