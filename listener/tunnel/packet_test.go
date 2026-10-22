package tunnel

import (
	"net"
	"testing"

	"github.com/metacubex/mihomo/listener/sing"
)

// T-095: replies of a tunnels: UDP listener go through its ReplyConn, so tunnel can batch them.
func TestPacketWriteBackThroughReplyConn(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	reply := sing.NewReplyConn(udp)
	var flushed int
	reply.InitFlush(func(*net.UDPConn) func([][]byte, []*net.UDPAddr) error {
		return func(d [][]byte, _ []*net.UDPAddr) error { flushed += len(d); return nil }
	})
	p := &packet{pc: udp, reply: reply, rAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}}
	if p.ReplyConn() != reply {
		t.Fatal("ReplyConn is not the listener's")
	}

	reply.Hold()
	for i := 0; i < 3; i++ {
		if n, err := p.WriteBack([]byte("x"), nil); n != 1 || err != nil {
			t.Fatalf("WriteBack: %d, %v", n, err)
		}
	}
	if err := reply.Release(); err != nil || flushed != 3 {
		t.Fatalf("flushed %d of 3, %v", flushed, err)
	}
	if (&packet{pc: udp}).ReplyConn() != nil {
		t.Fatal("a packet without ReplyConn must report none, not a typed nil")
	}
}
