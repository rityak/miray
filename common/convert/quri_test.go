package convert

import (
	"strings"
	"testing"
)

func TestQuriShareLink(t *testing.T) {
	link := "quri://" + strings.Repeat("q", 32) + "@relay.example:443?sni=relay.example&transports=h3,h2#MyRelay"
	proxies, err := ConvertsV2Ray([]byte(link))
	if err != nil || len(proxies) != 1 {
		t.Fatalf("quri subscription: proxies=%v error=%v", proxies, err)
	}
	if proxies[0]["type"] != "quri" || proxies[0]["name"] != "MyRelay" || proxies[0]["profile"] != link {
		t.Fatal("quri profile changed during subscription conversion")
	}
	if proxies, err := ConvertsV2Ray([]byte("quri://weak@relay.example:443")); err == nil || len(proxies) != 0 {
		t.Fatal("invalid quri link was not rejected")
	}
}
