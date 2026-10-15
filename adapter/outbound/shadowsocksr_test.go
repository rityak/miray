package outbound

import (
	"bytes"
	"io"
	"net"
	"testing"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/transport/socks5"
)

type ssrPacketReadStub struct {
	N.EnhancePacketConn
	data []byte
	put  func()
	err  error
}

func (c *ssrPacketReadStub) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	return c.data, c.put, nil, c.err
}

func TestSSRPacketConnReleasesBufferOnReadError(t *testing.T) {
	for _, hasBuffer := range []bool{false, true} {
		puts := 0
		stub := &ssrPacketReadStub{data: []byte("partial datagram"), err: io.ErrUnexpectedEOF}
		if hasBuffer {
			stub.put = func() { puts++ }
		}
		conn := &ssrPacketConn{EnhancePacketConn: stub}
		data, put, addr, err := conn.WaitReadFrom()
		wantPuts := 0
		if hasBuffer {
			wantPuts = 1
		}
		if err != io.ErrUnexpectedEOF || data != nil || put != nil || addr != nil || puts != wantPuts {
			t.Fatalf("hasBuffer=%t data=%v put=%t addr=%v err=%v puts=%d want=%d", hasBuffer, data, put != nil, addr, err, puts, wantPuts)
		}
		payload := []byte("next datagram")
		stub.data = append(socks5.ParseAddr("127.0.0.1:1234"), payload...)
		stub.err = nil
		data, put, addr, err = conn.WaitReadFrom()
		if err != nil || !bytes.Equal(data, payload) || addr == nil || addr.String() != "127.0.0.1:1234" || puts != wantPuts {
			t.Fatalf("read after error: data=%q addr=%v err=%v puts=%d", data, addr, err, puts)
		}
		if put != nil {
			put()
			if puts != wantPuts+1 {
				t.Fatalf("successful packet released %d times", puts-wantPuts)
			}
		}
	}
}
