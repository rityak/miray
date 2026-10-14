package shadowaead

import (
	"errors"
	"net"
	"testing"

	N "github.com/metacubex/mihomo/common/net"
)

type shortPacketConn struct {
	N.EnhancePacketConn
	data []byte
	puts int
}

func (c *shortPacketConn) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	return c.data, func() { c.puts++ }, &net.UDPAddr{}, nil
}

// A datagram shorter than salt+tag must be rejected, not panic (T-005).
func TestWaitReadFromShortPacket(t *testing.T) {
	ciph, err := AESGCM(make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	backing := make([]byte, 64*1024)
	for n := 0; n < ciph.SaltSize()+16; n++ {
		conn := &shortPacketConn{data: backing[:n]}
		pc := NewPacketConn(conn, ciph)
		data, put, _, err := pc.WaitReadFrom()
		if !errors.Is(err, ErrShortPacket) {
			t.Fatalf("len %d: err = %v, want ErrShortPacket", n, err)
		}
		if data != nil || put != nil || conn.puts != 1 {
			t.Fatalf("len %d: data=%v put=%v puts=%d, want buffer released once", n, data != nil, put != nil, conn.puts)
		}
	}
}
