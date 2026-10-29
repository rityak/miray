package dns

import (
	"context"
	"net/netip"
	"testing"

	icontext "github.com/metacubex/mihomo/context"
	D "github.com/miekg/dns"
)

func optCount(m *D.Msg) (n int) {
	for _, rr := range m.Extra {
		if rr.Header().Rrtype == D.TypeOPT {
			n++
		}
	}
	return
}

// ECS goes into the query's own OPT even when another record follows it: before,
// a second OPT was added (FORMERR, RFC 6891 §6.1.1); a new OPT gets a real size (T-156).
func TestEdns0SubnetOneOPT(t *testing.T) {
	subnet := netip.MustParsePrefix("203.0.113.0/24")
	m := new(D.Msg)
	m.SetQuestion("example.org.", D.TypeA)
	m.SetEdns0(4096, true)
	m.Extra = append(m.Extra, &D.TXT{Hdr: D.RR_Header{Name: "x.", Rrtype: D.TypeTXT, Class: D.ClassINET}, Txt: []string{"x"}})
	if !setEdns0Subnet(m, subnet, false) {
		t.Fatal("ECS not set")
	}
	if n := optCount(m); n != 1 {
		t.Fatalf("%d OPT records, want 1", n)
	}
	if opt := m.IsEdns0(); opt.UDPSize() != 4096 || !opt.Do() || len(opt.Option) != 1 {
		t.Fatalf("the client's OPT was not kept: %v", opt)
	}

	bare := new(D.Msg)
	bare.SetQuestion("example.org.", D.TypeA)
	setEdns0Subnet(bare, subnet, false)
	if opt := bare.IsEdns0(); opt == nil || opt.UDPSize() != edns0UDPSize {
		t.Fatalf("new OPT %v, want size %d", opt, edns0UDPSize)
	}
}

// The upstream's OPT never reaches the client: one without EDNS gets none, one
// with EDNS gets ours (RFC 6891 §6.1.1, T-156).
func TestServiceOwnOPT(t *testing.T) {
	s := &Service{handler: func(_ *icontext.DNSContext, m *D.Msg) (*D.Msg, error) {
		r := new(D.Msg)
		r.SetReply(m)
		r.SetEdns0(512, false) // the upstream's, as a cache miss returns it
		r.IsEdns0().Option = append(r.IsEdns0().Option, &D.EDNS0_SUBNET{Code: D.EDNS0SUBNET, Family: 1, SourceNetmask: 24, Address: netip.MustParseAddr("203.0.113.0").AsSlice()})
		return r, nil
	}}
	plain := new(D.Msg)
	plain.SetQuestion("example.org.", D.TypeA)
	r, err := s.ServeMsg(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	if n := optCount(r); n != 0 {
		t.Fatalf("a client without EDNS got %d OPT", n)
	}
	edns := new(D.Msg)
	edns.SetQuestion("example.org.", D.TypeA)
	edns.SetEdns0(4096, true)
	r, err = s.ServeMsg(context.Background(), edns)
	if err != nil {
		t.Fatal(err)
	}
	if opt := r.IsEdns0(); optCount(r) != 1 || opt.UDPSize() != edns0UDPSize || !opt.Do() || len(opt.Option) != 0 {
		t.Fatalf("reply OPT %v, want ours", opt)
	}
}
