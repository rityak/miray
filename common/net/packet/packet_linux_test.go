package packet

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// Queued datagrams come out in order with their sizes and source, several per
// recvmmsg; a sparse flow and the read deadline still work (T-051).
func TestEnhanceUDPConnBatch(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	c := NewEnhancePacketConn(udp).(*enhanceUDPConn)
	sender, err := net.DialUDP("udp", nil, udp.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))

	// sparse: one datagram, then the batch must not wait for more
	_, _ = sender.Write([]byte{9})
	data, put, addr, err := c.WaitReadFrom()
	if err != nil || len(data) != 1 || data[0] != 9 || addr.String() != sender.LocalAddr().String() {
		t.Fatalf("sparse read: %v %v %v", data, addr, err)
	}
	put()

	const count = 30
	for i := 0; i < count; i++ {
		_, _ = sender.Write(make([]byte, 10+i))
	}
	time.Sleep(50 * time.Millisecond) // let them queue
	batched := false
	for i := 0; i < count; i++ {
		data, put, _, err := c.WaitReadFrom()
		if err != nil {
			t.Fatalf("datagram %d: %v", i, err)
		}
		if len(data) != 10+i {
			t.Fatalf("datagram %d has %d bytes, want %d", i, len(data), 10+i)
		}
		put()
		batched = batched || len(c.queue) > 0
	}
	if !batched {
		t.Fatal("queued datagrams were read one per syscall")
	}

	_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, _, err := c.WaitReadFrom(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("idle read: %v, want a deadline error", err)
	}
}
