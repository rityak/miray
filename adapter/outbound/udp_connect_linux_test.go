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
