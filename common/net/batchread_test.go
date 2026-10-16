package net

import (
	"net"
	"runtime"
	"testing"
	"time"
)

// Queued datagrams come out in order with their sizes and sources; on Linux
// several per call (T-060).
func TestBatchReader(t *testing.T) {
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
	const count = 10
	for i := 0; i < count; i++ {
		if _, err := sender.Write(make([]byte, 100+i)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond) // let all of them queue

	r := NewBatchReader(conn, 16)
	bufs := make([][]byte, 16)
	for i := range bufs {
		bufs[i] = make([]byte, 2048)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, calls := 0, 0
	for got < count {
		n, err := r.Read(bufs)
		if err != nil {
			t.Fatalf("after %d datagrams: %v", got, err)
		}
		calls++
		for i := 0; i < n; i++ {
			if r.Len(i) != 100+got || r.Addr(i).String() != sender.LocalAddr().String() {
				t.Fatalf("datagram %d: len %d from %v", got, r.Len(i), r.Addr(i))
			}
			got++
		}
	}
	if runtime.GOOS == "linux" && calls > 2 {
		t.Fatalf("%d queued datagrams took %d reads, want a batch", count, calls)
	}
}
