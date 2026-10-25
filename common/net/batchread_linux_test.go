//go:build linux

package net

import (
	"net"
	"testing"
	"time"
)

// A batch of queued datagrams is read without allocating per datagram: x/net packed every
// slot and allocated an address for each (T-128, as the connected reader of T-096).
func TestBatchReaderNoAllocs(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sender, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	r := NewBatchReader(conn, 8)
	bufs := make([][]byte, 8)
	for i := range bufs {
		bufs[i] = make([]byte, 2048)
	}
	payload := make([]byte, 100)
	allocs := testing.AllocsPerRun(50, func() {
		for i := 0; i < 8; i++ {
			_, _ = sender.Write(payload)
		}
		time.Sleep(time.Millisecond)
		for got := 0; got < 8; {
			n, err := r.Read(bufs)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				_ = r.AddrPort(i)
			}
			got += n
		}
	})
	if allocs > 1 {
		t.Fatalf("%.1f allocations per batch of 8, want none", allocs)
	}
}
