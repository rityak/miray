package tunnel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// rejectUDPProxy is REJECT for UDP: a session that takes datagrams and drops them.
type rejectUDPProxy struct{ lifecycleProxy }

type rejectPC struct{ C.PacketConn }

func (pc *rejectPC) Chains() C.Chain { return C.Chain{"REJECT"} }

func (p *rejectUDPProxy) ListenPacketContext(ctx context.Context, m *C.Metadata) (C.PacketConn, error) {
	pc, err := p.lifecycleProxy.ListenPacketContext(ctx, m)
	if err != nil {
		return nil, err
	}
	return &rejectPC{pc}, nil
}

// refusalPacket is a TUN datagram whose inbound can refuse it (T-154).
type refusalPacket struct {
	benchPacket
	refused chan error
}

func (p *refusalPacket) HandshakeFailure(err error) error { p.refused <- err; return nil }

// A UDP session whose dial fails or whose rule is REJECT tells the inbound of its
// first datagram, which answers the app with ICMP port unreachable (T-154); the
// REJECT session stays and takes the rest of the flow without a new refusal.
func TestUDPRefusalReported(t *testing.T) {
	oldProxies, oldMode := proxies, mode.Load()
	defer func() { proxies = oldProxies; mode.Store(oldMode) }()
	mode.Store(Direct)
	OnRunning()

	dst := netip.MustParseAddrPort("192.0.2.7:443")
	send := func(src netip.AddrPort, p *refusalPacket) {
		p.addr = net.UDPAddrFromAddrPort(src)
		Tunnel.HandleUDPPacket(p, &C.Metadata{NetWork: C.UDP, Type: C.TUN,
			SrcIP: src.Addr(), SrcPort: src.Port(), DstIP: dst.Addr(), DstPort: dst.Port()})
	}
	refused := func(p *refusalPacket) error {
		t.Helper()
		select {
		case err := <-p.refused:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("the refusal was not reported")
			return nil
		}
	}

	failing := &lifecycleProxy{}
	failing.fail.Store(true)
	proxies = map[string]C.Proxy{"DIRECT": failing}
	p := &refusalPacket{benchPacket: benchPacket{data: []byte{1}}, refused: make(chan error, 4)}
	send(netip.MustParseAddrPort("198.18.0.1:40011"), p)
	if err := refused(p); err == nil {
		t.Fatal("refused without the dial error")
	}

	proxies = map[string]C.Proxy{"DIRECT": &rejectUDPProxy{}}
	src := netip.MustParseAddrPort("198.18.0.1:40012")
	first := &refusalPacket{benchPacket: benchPacket{data: []byte{2}}, refused: make(chan error, 4)}
	send(src, first)
	if err := refused(first); !errors.Is(err, errRejected) {
		t.Fatalf("refusal %v, want the rejection", err)
	}
	eventually(t, "the REJECT session ended", func() bool {
		_, ok := natTable.Get(first.addr.String())
		return ok
	})
	later := &refusalPacket{benchPacket: benchPacket{data: []byte{3}}, refused: make(chan error, 4)}
	send(src, later)
	select {
	case err := <-later.refused:
		t.Fatalf("a later datagram of the REJECT session refused again: %v", err)
	case err := <-first.refused:
		t.Fatalf("refused twice: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

// The idle timeout of a session (T-154): 5 min by default (RFC 4787 REQ-5), 10 s to
// the ports of DNS, NTP and STUN as in sing-box, mihomo's 60 s for all with 0.
func TestUDPSessionTimeout(t *testing.T) {
	old := udpTimeout.Load()
	defer udpTimeout.Store(old)
	for _, c := range []struct {
		set  time.Duration
		port uint16
		want time.Duration
	}{
		{5 * time.Minute, 443, 5 * time.Minute},
		{5 * time.Minute, 53, 10 * time.Second},
		{5 * time.Minute, 123, 10 * time.Second},
		{5 * time.Minute, 3478, 10 * time.Second},
		{5 * time.Second, 53, 5 * time.Second},
		{0, 443, 60 * time.Second},
		{0, 53, 60 * time.Second},
	} {
		SetUDPTimeout(c.set)
		if got := udpSessionTimeout(c.port); got != c.want {
			t.Errorf("timeout %v, port %d: %v, want %v", c.set, c.port, got, c.want)
		}
	}
}
