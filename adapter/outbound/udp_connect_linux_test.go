//go:build linux

package outbound

import (
	"net"
	"testing"
	"time"
)

// A connected session socket sends to the server whatever address WriteTo
// gets, receives its replies, and ignores datagrams from anyone else (T-061).
func TestConnectUDP(t *testing.T) {
	for _, network := range []string{"udp", "udp4"} { // dual-stack (v4-mapped server) and AF_INET
		server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		stranger, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := net.ListenUDP(network, nil)
		if err != nil {
			t.Fatal(err)
		}
		saddr := server.LocalAddr().(*net.UDPAddr)
		pc := connectUDP(raw, saddr)
		if _, ok := pc.(*connectedUDPConn); !ok {
			t.Fatalf("%s: socket not connected", network)
		}
		if _, err := pc.WriteTo([]byte("ping"), stranger.LocalAddr()); err != nil { // the address is ignored
			t.Fatal(network, err)
		}
		buf := make([]byte, 16)
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, from, err := server.ReadFromUDP(buf)
		if err != nil || string(buf[:n]) != "ping" {
			t.Fatalf("%s: server got %q %v", network, buf[:n], err)
		}
		_, _ = stranger.WriteToUDP([]byte("spoof"), from)
		_, _ = server.WriteToUDP([]byte("pong"), from)
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		if n, _, err = pc.ReadFrom(buf); err != nil || string(buf[:n]) != "pong" {
			t.Fatalf("%s: client got %q %v, want only the server's reply", network, buf[:n], err)
		}
		_ = pc.Close()
		_ = server.Close()
		_ = stranger.Close()
	}
}

// While corked, writes are kept as copies and nothing is sent (T-059).
func TestConnectedUDPCork(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	raw, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := connectUDP(raw, server.LocalAddr()).(*connectedUDPConn)
	defer c.Close()
	b := []byte{1, 2, 3}
	c.Cork()
	for i := 0; i < 3; i++ {
		b[0] = byte(i)
		if n, err := c.WriteTo(b, nil); n != 3 || err != nil {
			t.Fatal(n, err)
		}
	}
	got := c.Uncork()
	if len(got) != 3 || got[0][0] != 0 || got[2][0] != 2 {
		t.Fatalf("collected %v", got)
	}
	_ = server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := server.ReadFrom(make([]byte, 16)); err == nil {
		t.Fatal("a corked write was sent")
	}
	if _, err := c.WriteTo([]byte{7}, nil); err != nil { // uncorked: sent at once
		t.Fatal(err)
	}
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 16)
	if n, _, err := server.ReadFrom(buf); err != nil || n != 1 || buf[0] != 7 {
		t.Fatalf("uncorked write: %v %v", buf[:n], err)
	}
}
