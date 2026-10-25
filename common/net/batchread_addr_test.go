package net

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

// AddrPort is the source of datagram i, unmapped, for IPv4 and IPv6 listeners (T-128).
func TestBatchReaderAddrPort(t *testing.T) {
	for _, local := range []string{"127.0.0.1:0", "[::1]:0"} {
		conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort(local)))
		if err != nil {
			t.Skipf("%s: %v", local, err)
		}
		sender, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if _, err := sender.Write(make([]byte, 50+i)); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(20 * time.Millisecond)
		r := NewBatchReader(conn, 8)
		bufs := make([][]byte, 8)
		for i := range bufs {
			bufs[i] = make([]byte, 2048)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		want := sender.LocalAddr().(*net.UDPAddr).AddrPort()
		for got := 0; got < 3; {
			n, err := r.Read(bufs)
			if err != nil {
				t.Fatalf("%s: %v", local, err)
			}
			for i := 0; i < n; i++ {
				if ap := r.AddrPort(i); ap != want || r.Len(i) != 50+got {
					t.Fatalf("%s: datagram %d: %v len %d, want %v", local, got, ap, r.Len(i), want)
				}
				got++
			}
		}
		sender.Close()
		conn.Close()
	}
}
