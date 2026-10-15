package dns

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
)

func TestSystemDNSResetRefreshesWindowsAdapters(t *testing.T) {
	nameservers, err := dnsReadConfig()
	if err != nil {
		t.Fatalf("enumerate Windows DNS adapters: %v", err)
	}
	expected := make(map[string]bool)
	for _, address := range nameservers {
		if !resolver.IsSystemDnsBlacklisted(address) {
			expected["udp://"+net.JoinHostPort(address, "53")] = true
		}
	}
	if len(expected) == 0 {
		t.Skip("no active Windows adapter has a usable system DNS server")
	}

	client := newSystemClient()
	client.lastFlush = time.Now()
	const obsolete = "192.0.2.1"
	client.dnsClients[obsolete] = &systemDnsClient{
		dnsClient: newClient(net.JoinHostPort(obsolete, "53"), nil, "udp", nil, nil, ""),
	}
	client.ResetConnection()
	refreshed, err := client.getDnsClients()
	if err != nil {
		t.Fatal(err)
	}
	for _, dnsClient := range refreshed {
		address := dnsClient.Address()
		if strings.Contains(address, obsolete) {
			t.Fatal("obsolete nameserver remained active after an interface reset")
		}
		if !expected[address] {
			t.Fatalf("unexpected adapter nameserver %q", address)
		}
		delete(expected, address)
	}
	if len(expected) != 0 {
		t.Fatalf("current adapter nameservers were not loaded: %v", expected)
	}
}
