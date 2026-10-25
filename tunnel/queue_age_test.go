package tunnel

import (
	"testing"
	"time"
)

// A new session's datagrams wait while its proxy is dialed: the queue holds what a
// fast source sends in that time (50 Mbit/s of 1200-byte datagrams is ~5000 a second;
// the old 128 dropped ~220 at the start of every QUIC session, T-092), and a datagram
// that waited longer than maxQueueDelay is dropped on its way out instead of being
// sent late (T-130, user decision: by time, not count; RFC 8289, RFC 7567).
func TestPacketSenderQueueByAge(t *testing.T) {
	now := int64(0)
	queueClock = func() int64 { return now }
	defer func() { queueClock = monotonic }()

	sender := newPacketSender().(*packetSender)
	defer sender.Close()
	const burst = 1000 // ~200 ms of 50 Mbit/s
	stale := make([]releaseCountingPacket, burst)
	for i := range stale {
		sender.Send(&stale[i])
	}
	if len(sender.ch) != burst {
		t.Fatalf("queued %d of a %d-datagram burst", len(sender.ch), burst)
	}
	for i := range stale {
		if stale[i].drops.Load() != 0 {
			t.Fatalf("datagram %d of the burst dropped on the way in", i)
		}
	}

	now += int64(maxQueueDelay + time.Millisecond)
	fresh := &releaseCountingPacket{}
	sender.Send(fresh)

	var out []senderItem
	for len(sender.ch) > 0 {
		if item, ok := sender.fresh(<-sender.ch, queueClock()); ok {
			out = append(out, item)
		}
	}
	if len(out) != 1 || out[0] != fresh {
		t.Fatalf("%d datagrams came out, want only the fresh one", len(out))
	}
	for i := range stale {
		if stale[i].drops.Load() != 1 {
			t.Fatalf("stale datagram %d: %d drops, want 1", i, stale[i].drops.Load())
		}
	}
}
