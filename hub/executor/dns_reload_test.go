package executor

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/metacubex/mihomo/component/resolver"

	D "github.com/miekg/dns"
)

// A reload with an unchanged dns section keeps the answer cache; a changed
// section starts with a fresh one (T-025).
func TestReloadKeepsDNSCacheForUnchangedSection(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int32
	server := &D.Server{PacketConn: conn, Handler: D.HandlerFunc(func(w D.ResponseWriter, r *D.Msg) {
		queries.Add(1)
		m := new(D.Msg)
		m.SetReply(r)
		if r.Question[0].Qtype == D.TypeA {
			m.Answer = []D.RR{&D.A{Hdr: D.RR_Header{Name: r.Question[0].Name, Rrtype: D.TypeA, Class: D.ClassINET, Ttl: 300}, A: net.IPv4(192, 0, 2, 7)}}
		}
		_ = w.WriteMsg(m)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	previous := resolver.DefaultResolver
	t.Cleanup(func() { resolver.DefaultResolver = previous; dnsKey = "" })

	apply := func(nameserver string) {
		t.Helper()
		cfg, err := ParseWithBytes([]byte(fmt.Sprintf("dns:\n  enable: true\n  ipv6: false\n  nameserver: [%q]\n", nameserver)))
		if err != nil {
			t.Fatal(err)
		}
		updateDNS(cfg.DNS, false, "")
	}
	lookup := func() {
		t.Helper()
		ips, err := resolver.DefaultResolver.LookupIPv4(context.Background(), "cached.example")
		if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr("192.0.2.7") {
			t.Fatalf("lookup = %v, %v", ips, err)
		}
	}
	address := "udp://" + conn.LocalAddr().String()

	apply(address)
	lookup()
	apply(address) // reload, same dns section
	lookup()
	if got := queries.Load(); got != 1 {
		t.Fatalf("queries after an unchanged reload = %d, want 1 (cache kept)", got)
	}
	apply(address + "#disable-ipv6=true") // changed section
	lookup()
	if got := queries.Load(); got != 2 {
		t.Fatalf("queries after a changed reload = %d, want 2 (fresh cache)", got)
	}
}

// ApplyConfig keeps the open DNS connections only when the clients were kept
// and nothing they depend on changed: interface or routing mark, or a server
// reached through a proxy, which the reload may have replaced (T-056).
func TestReloadKeepsDNSConnectionsOnlyWhenSafe(t *testing.T) {
	previous := resolver.DefaultResolver
	t.Cleanup(func() { resolver.DefaultResolver = previous; dnsKey, dnsDialKey, dnsWarm = "", "", false })
	apply := func(nameserver, dialKey string) bool {
		t.Helper()
		cfg, err := ParseWithBytes([]byte(fmt.Sprintf("dns:\n  enable: true\n  ipv6: false\n  nameserver: [%q]\n", nameserver)))
		if err != nil {
			t.Fatal(err)
		}
		updateDNS(cfg.DNS, false, dialKey)
		return dnsWarm
	}
	const plain, proxied = "tls://192.0.2.53", "tls://192.0.2.53#PROXY"
	if apply(plain, "eth0/0") {
		t.Fatal("first start: nothing to keep")
	}
	if !apply(plain, "eth0/0") {
		t.Fatal("unchanged reload must keep the connections")
	}
	if apply(plain, "eth1/0") {
		t.Fatal("a changed interface must reset the connections")
	}
	if apply(proxied, "eth1/0") || apply(proxied, "eth1/0") {
		t.Fatal("a server behind a proxy must reset the connections")
	}
}
