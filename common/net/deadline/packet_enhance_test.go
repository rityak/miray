package deadline

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// blockingPC answers WaitReadFrom from a channel, or blocks until closed.
type blockingPC struct {
	net.PacketConn
	data chan []byte
}

func (c *blockingPC) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	b, ok := <-c.data
	if !ok {
		return nil, nil, nil, net.ErrClosed
	}
	return b, nil, &net.UDPAddr{}, nil
}

func (c *blockingPC) SetReadDeadline(time.Time) error { return errors.New("no native deadline") }

// T-096: without a read deadline WaitReadFrom reads directly (the token stays put); a deadline set
// later still interrupts a read through the pipe, as before.
func TestEnhanceWaitReadFromDirectUntilDeadline(t *testing.T) {
	inner := &blockingPC{data: make(chan []byte, 1)}
	pc := NewEnhancePacketConn(inner).(*EnhancePacketConn)

	inner.data <- []byte("a")
	if b, _, _, err := pc.WaitReadFrom(); err != nil || string(b) != "a" {
		t.Fatalf("direct read: %q, %v", b, err)
	}
	if len(pc.resultCh) != 1 || pc.inRead.Load() {
		t.Fatal("a direct read must not take the token or stay in read")
	}

	if err := pc.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, _, err := pc.WaitReadFrom(); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("after SetReadDeadline: %v, want deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the deadline did not interrupt the read")
	}
	close(inner.data) // ends the pipe read left behind
}
