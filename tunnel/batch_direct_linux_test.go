//go:build linux

package tunnel_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// A real DIRECT session must get the sendmmsg path; this breaks loudly if an
// upstream rename of a wrapper type silently disables batching (T-014).
func TestDirectSessionGetsBatchWriter(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	dst := sink.LocalAddr().(*net.UDPAddr).AddrPort()
	metadata := &C.Metadata{NetWork: C.UDP, DstIP: dst.Addr(), DstPort: dst.Port(), SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 40000}

	pc, err := outbound.NewDirect().ListenPacketContext(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	tracked := statistic.NewUDPTracker(pc, statistic.DefaultManager, metadata, nil, 0, 0, true)
	defer tracked.Close()
	if !tunnel.HasBatchWriter(tracked) {
		t.Fatal("DIRECT session has no batch writer")
	}
}
