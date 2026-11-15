package adapter

import (
	"strings"
	"testing"
)

func TestParseQuriProxy(t *testing.T) {
	proxy, err := ParseProxy(map[string]any{"type": "quri", "name": "relay", "server": "relay.example", "port": 443, "token": strings.Repeat("q", 32), "sni": "relay.example", "transports": []string{"h3", "h2"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := proxy.Close(); err != nil {
			t.Error(err)
		}
	}()
	if proxy.Type().String() != "Quri" || proxy.Addr() != "relay.example:443" || !proxy.SupportUDP() {
		t.Fatalf("wrong quri adapter: type=%s address=%s udp=%v", proxy.Type(), proxy.Addr(), proxy.SupportUDP())
	}
	if _, err := ParseProxy(map[string]any{"type": "quri", "name": "invalid", "server": "relay.example", "port": 443, "token": "weak"}); err == nil {
		t.Fatal("accepted a weak quri credential")
	}
}
