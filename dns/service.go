package dns

import (
	"context"
	"errors"

	"github.com/metacubex/mihomo/component/resolver"
	icontext "github.com/metacubex/mihomo/context"
	D "github.com/miekg/dns"
)

// edns0UDPSize is the fragmentation-safe UDP payload size of
// https://dnsflagday.net/2020/ : the IPv6 minimum MTU (1280) minus the IPv6 and
// UDP headers (40 + 8).
const edns0UDPSize = 1232

type Service struct {
	handler handler
}

// ServeMsg implement [resolver.Service] ResolveMsg
func (s *Service) ServeMsg(ctx context.Context, msg *D.Msg) (*D.Msg, error) {
	if len(msg.Question) == 0 {
		return nil, errors.New("at least one question is required")
	}

	r, err := s.handler(icontext.NewDNSContext(ctx), msg)
	if err != nil {
		return r, err
	}

	// echo back EDNS0: if the client asked with an OPT, reply with our own OPT.
	// OPT is a per-hop pseudo-record (RFC 6891 §6.1.1), so we generate it here
	// instead of caching/forwarding the upstream one: a cache miss passed it on, to a
	// client without EDNS too (T-156). Doing it at this convergence point covers
	// both the listening DNS server (ServeDNS) and the TUN hijack relay path.
	if r != nil {
		extra := r.Extra[:0]
		for _, rr := range r.Extra {
			if rr.Header().Rrtype != D.TypeOPT {
				extra = append(extra, rr)
			}
		}
		r.Extra = extra
		if reqOpt := msg.IsEdns0(); reqOpt != nil {
			r.SetEdns0(edns0UDPSize, reqOpt.Do())
		}
	}

	return r, nil
}

var _ resolver.Service = (*Service)(nil)

func NewService(resolver resolver.Resolver, mapper *ResolverEnhancer) *Service {
	return &Service{handler: newHandler(resolver, mapper)}
}
