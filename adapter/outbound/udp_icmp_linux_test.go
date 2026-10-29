//go:build linux

package outbound

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// An ICMP port unreachable from the server (it restarts) does not end a UDP
// session over the connected socket: RFC 4787 REQ-11 forbids an ICMP to destroy
// the mapping, and an unconnected socket, as in mihomo, never sees it (T-154).
func TestConnectedUDPSurvivesICMP(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close() // the port is closed: a datagram to it brings back ICMP port unreachable

	ss, err := NewShadowSocks(ShadowSocksOption{Name: "ss", Server: "127.0.0.1", Port: port, Cipher: "none", UDP: true})
	if err != nil {
		t.Fatal(err)
	}
	md := &C.Metadata{NetWork: C.UDP, DstIP: netip.MustParseAddr("192.0.2.1"), DstPort: 9}
	pc, err := ss.ListenPacketContext(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	// the session's reader waits on the socket all the time, as handleUDPToLocal does
	type result struct {
		data string
		err  error
	}
	read := make(chan result, 1)
	go func() {
		data, put, _, err := pc.WaitReadFrom()
		read <- result{string(data), err}
		if put != nil {
			put()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	dst := md.UDPAddr()
	if _, err := pc.WriteTo([]byte("lost"), dst); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the ICMP has come back

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { // "none" carries the address with the payload: echo it back as is
		buf := make([]byte, 2048)
		for {
			n, from, err := server.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = server.WriteToUDP(buf[:n], from)
		}
	}()
	if _, err := pc.WriteTo([]byte("ping"), dst); err != nil {
		t.Fatalf("write after the ICMP: %v", err)
	}
	select {
	case r := <-read:
		if r.err != nil || r.data != "ping" {
			t.Fatalf("read after the ICMP: %q %v, want the echo: the session goes on", r.data, r.err)
		}
	case <-time.After(3 * time.Second):
		_ = pc.Close()
		t.Fatal("no echo")
	}
}

// A send that meets the error of an earlier ICMP is not lost: the kernel reports
// the error instead of sending, and the datagram goes again (T-154).
func TestConnectedUDPWriteAfterICMP(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.LocalAddr().(*net.UDPAddr)
	_ = probe.Close()
	raw, err := net.ListenUDP("udp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	pc := connectUDP(raw, addr)
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("lost"), nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	server, err := net.ListenUDP("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := pc.WriteTo([]byte("ping"), nil); err != nil {
		t.Fatalf("write after the ICMP: %v", err)
	}
	buf := make([]byte, 16)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	if n, _, err := server.ReadFromUDP(buf); err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("server got %q %v", buf[:n], err)
	}
}
