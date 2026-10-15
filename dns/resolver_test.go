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
