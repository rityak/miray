package executor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

// On a reload, providers of the new configuration load while the previous one
// keeps serving: a slow provider download must not keep the tunnel out of
// Running (D-006, T-026).
func TestReloadKeepsTunnelRunningWhileProvidersLoad(t *testing.T) {
	const slow = 1500 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(slow)
		}
		fmt.Fprintf(w, "proxies:\n  - {name: %s, type: socks5, server: 127.0.0.1, port: 1}\n", r.URL.Path[1:])
	}))
	t.Cleanup(server.Close)

	// Not t.TempDir: the profile cache file stays open process-wide, which
	// blocks its removal on Windows.
	dir, err := os.MkdirTemp("", "miray-reload")
	if err != nil {
		t.Fatal(err)
	}
	home := C.Path.HomeDir()
	C.SetHomeDir(dir)
	t.Cleanup(func() { C.SetHomeDir(home); _ = os.RemoveAll(dir) })

	config := func(providers ...string) string {
		s := "log-level: silent\nfind-process-mode: off\nproxy-providers:\n"
		for _, name := range providers {
			s += fmt.Sprintf("  %s: {type: http, url: %q, path: ./%s.yaml, interval: 3600}\n", name, server.URL+"/"+name, name)
		}
		s += "proxy-groups:\n  - {name: g, type: select, use: ["
		for i, name := range providers {
			if i > 0 {
				s += ", "
			}
			s += name
		}
		return s + "]}\nrules:\n  - MATCH,DIRECT\n"
	}
	apply := func(text string, force bool) {
		t.Helper()
		cfg, err := ParseWithBytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		ApplyConfig(cfg, force)
	}

	apply(config("fast"), true) // first start: providers load in the Inner phase
	if tunnel.Status() != tunnel.Running {
		t.Fatalf("status after the first start = %v", tunnel.Status())
	}

	done := make(chan time.Duration)
	stop := make(chan struct{})
	go func() {
		var notRunning time.Duration
		last := time.Now()
		for {
			select {
			case <-stop:
				done <- notRunning
				return
			case <-time.After(2 * time.Millisecond):
			}
			now := time.Now()
			if tunnel.Status() != tunnel.Running {
				notRunning += now.Sub(last)
			}
			last = now
		}
	}()
	started := time.Now()
	apply(config("fast", "slow"), false)
	elapsed := time.Since(started)
	close(stop)
	notRunning := <-done

	if elapsed < slow {
		t.Fatalf("reload took %v, the slow provider was not loaded", elapsed)
	}
	if notRunning > slow/3 {
		t.Fatalf("tunnel was not running for %v of a %v reload", notRunning, elapsed)
	}
	if pv := tunnel.Providers()["slow"]; pv == nil || pv.Count() != 1 {
		t.Fatalf("slow provider not loaded: %v", pv)
	}
	t.Logf("tunnel not running for %v of a %v reload", notRunning, elapsed)

	// A provider whose proxy exists only in the new configuration cannot load
	// through the previous one; it is retried once the new one is running.
	text := config("fast", "slow", "late") + "proxies:\n  - {name: only-new, type: direct}\n"
	text = strings.Replace(text, "path: ./late.yaml,", "path: ./late.yaml, proxy: only-new,", 1)
	apply(text, false)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if pv := tunnel.Providers()["late"]; pv != nil && pv.Count() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider behind a proxy of the new configuration was not retried after the reload")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
