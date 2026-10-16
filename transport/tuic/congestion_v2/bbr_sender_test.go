package congestion

import (
	"testing"
	"time"
)

// PROBE_RTT keeps 3/4 of the BDP in flight instead of 4 packets (T-069).
func TestProbeRttCongestionWindow(t *testing.T) {
	b := NewBbrSender(1200, 32, ProfileStandard)
	b.minRtt = 40 * time.Millisecond // PROBE_RTT starts only once min_rtt expired, so it is set
	if got := b.probeRttCongestionWindow(); got != 28_800 {
		t.Fatalf("no bandwidth sample: %d, want 28800 (0.75 of the initial window)", got)
	}
	b.maxBandwidth.Update(100_000_000*BitsPerSecond, 1) // 100 Mbit/s: BDP 500 000 bytes
	if got := b.probeRttCongestionWindow(); got != 375_000 {
		t.Fatalf("PROBE_RTT window %d, want 375000 (0.75 BDP)", got)
	}
}
