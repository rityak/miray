package sing

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
)

func TestReplyConnHold(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	dst := M.SocksaddrFromNetIP(peer.LocalAddr().(*net.UDPAddr).AddrPort())

	c := NewReplyConn(udp)
	var flushed [][]byte
	var addrs []*net.UDPAddr
	c.InitFlush(func(*net.UDPConn) func([][]byte, []*net.UDPAddr) error {
		return func(d [][]byte, a []*net.UDPAddr) error {
			for _, b := range d {
				flushed = append(flushed, append([]byte(nil), b...))
			}
			addrs = append(addrs, a...)
			return nil
		}
	})
	write := func(s string) {
		if err := c.WritePacket(buf.As([]byte(s)).ToOwned(), dst); err != nil {
			t.Fatal(err)
		}
	}

	c.Hold()
	c.Hold() // a second session holds the shared socket
	write("a")
	write("b")
	if err := c.Release(); err != nil || flushed != nil {
		t.Fatalf("flushed %q while another session holds", flushed)
	}
	write("c")
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	if len(flushed) != 3 || string(flushed[0]) != "a" || string(flushed[1]) != "b" || string(flushed[2]) != "c" {
		t.Fatalf("flushed %q, want a b c in order", flushed)
	}
	if addrs[0] != addrs[1] || addrs[1] != addrs[2] || addrs[0].AddrPort() != dst.AddrPort() {
		t.Fatalf("addrs %v: one pointer per client run is needed for GSO", addrs)
	}

	write("d") // nobody holds: straight to the socket
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 16)
	n, from, err := peer.ReadFromUDPAddrPort(b)
	if err != nil || string(b[:n]) != "d" || from != netip.MustParseAddrPort(udp.LocalAddr().String()) {
		t.Fatalf("direct write: %q from %v, %v", b[:n], from, err)
	}
	if len(flushed) != 3 {
		t.Fatalf("a direct write went through the batch")
	}
}
