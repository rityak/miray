package dns

import (
	"context"
	"net/netip"
	"testing"
	"time"

	D "github.com/miekg/dns"
)

// ipv6OnlyClient answers A with no records at once and AAAA after a delay.
type ipv6OnlyClient struct{ aaaaDelay time.Duration }

func (c ipv6OnlyClient) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	q := m.Question[0]
	msg := new(D.Msg)
	msg.SetReply(m)
	if q.Qtype == D.TypeAAAA {
		time.Sleep(c.aaaaDelay)
		msg.Answer = []D.RR{&D.AAAA{
			Hdr:  D.RR_Header{Name: q.Name, Rrtype: D.TypeAAAA, Class: D.ClassINET, Ttl: 60},
			AAAA: netip.MustParseAddr("2001:db8::1").AsSlice(),
		}}
	}
	return msg, nil
}

func (ipv6OnlyClient) Address() string  { return "fake" }
func (ipv6OnlyClient) ResetConnection() {}

// An IPv6-only name must resolve even when AAAA arrives after ipv6-timeout:
// with A failed there is nothing else to return (T-021).
func TestLookupIPWaitsForAAAAWhenAFails(t *testing.T) {
	r := NewResolverFromClient(ipv6OnlyClient{aaaaDelay: 300 * time.Millisecond})
	r.ipv6Timeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := r.LookupIP(ctx, "v6only.example")
	if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("LookupIP = %v, %v; want [2001:db8::1]", ips, err)
	}
}

// aClient answers A with one address.
type aClient struct{ addr netip.Addr }

func (c aClient) ExchangeContext(_ context.Context, m *D.Msg) (*D.Msg, error) {
	msg := new(D.Msg)
	msg.SetReply(m)
	msg.Answer = []D.RR{&D.A{Hdr: D.RR_Header{Name: m.Question[0].Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 60}, A: c.addr.AsSlice()}}
	return msg, nil
}

func (aClient) Address() string  { return "fake" }
func (aClient) ResetConnection() {}

// A stale record is served at once while refreshed, but not past maxStale after
// its expiry: then the name is asked again first (RFC 8767 §5, T-156).
func TestStaleRecordLimit(t *testing.T) {
	stale, fresh := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
	for _, c := range []struct {
		expired time.Duration
		want    netip.Addr
	}{{time.Hour, stale}, {maxStale + time.Hour, fresh}} {
		r := NewResolverFromClient(aClient{fresh})
		q := D.Question{Name: "stale.example.", Qtype: D.TypeA, Qclass: D.ClassINET}
		old := new(D.Msg)
		old.SetQuestion(q.Name, q.Qtype)
		old.Answer = []D.RR{&D.A{Hdr: D.RR_Header{Name: q.Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 60}, A: stale.AsSlice()}}
		r.cache.SetWithExpire(q.String(), old, time.Now().Add(-c.expired))
		m := new(D.Msg)
		m.SetQuestion(q.Name, q.Qtype)
		msg, err := r.ExchangeContext(context.Background(), m)
		if err != nil || len(msg.Answer) != 1 {
			t.Fatalf("expired %v: %v %v", c.expired, msg, err)
		}
		if got, _ := netip.AddrFromSlice(msg.Answer[0].(*D.A).A); got != c.want {
			t.Errorf("expired %v: %v, want %v", c.expired, got, c.want)
		}
	}
}
