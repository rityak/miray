//go:build !(android && cmfa)

package dns

import (
	"testing"
	"time"
)

func TestSystemDNSResetInvalidatesNameservers(t *testing.T) {
	client := newSystemClient()
	client.lastFlush = time.Now()
	resolver := &Resolver{main: []dnsClient{client}}
	resolver.ResetConnection()
	if !client.lastFlush.IsZero() {
		t.Fatal("system nameserver cache remained fresh after resolver reset")
	}
}
