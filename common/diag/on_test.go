//go:build miraydiag

package diag

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The tests share one journal: a sink collects its lines.
var (
	sinkMu sync.Mutex
	lines  []map[string]any
)

func init() {
	sink = func(_ int, line []byte) {
		var m map[string]any
		if json.Unmarshal(line, &m) == nil {
			sinkMu.Lock()
			lines = append(lines, m)
			sinkMu.Unlock()
		}
	}
	start(Config{})
	SetLookups(func(name string) string {
		switch name {
		case "DIRECT":
			return "Direct"
		case "auto":
			return "URLTest"
		}
		return "Vless"
	}, func() (int, int) { return 0, 0 })
}

// collect returns the records of kind written after mark, waiting for the writer.
func collect(t *testing.T, kind string, mark int) []map[string]any {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	sinkMu.Lock()
	defer sinkMu.Unlock()
	var out []map[string]any
	for _, m := range lines[mark:] {
		if m["k"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func mark() int {
	time.Sleep(20 * time.Millisecond)
	sinkMu.Lock()
	defer sinkMu.Unlock()
	limiter.roll()
	return len(lines)
}

func TestDestinationIsRegistrableDomainOrHash(t *testing.T) {
	if got := dst("rr3---sn-abc.googlevideo.com", netip.Addr{}, 443); got != "googlevideo.com:443" {
		t.Fatalf("domain: %s", got)
	}
	got := dst("", netip.MustParseAddr("203.0.113.7"), 443)
	if !strings.HasPrefix(got, "ip4:") || strings.Contains(got, "203.0") {
		t.Fatalf("ip: %s", got)
	}
	if dst("", netip.MustParseAddr("203.0.113.7"), 443) != got {
		t.Fatal("one address must hash the same within a run")
	}
	if got := dst("", netip.MustParseAddr("192.168.1.10"), 53); !strings.HasPrefix(got, "lan:") {
		t.Fatalf("lan: %s", got)
	}
}

func TestScrubTakesAddressesOut(t *testing.T) {
	for _, s := range []string{
		"dial tcp 203.0.113.7:443: connect: connection refused",
		"dial tcp [2001:db8::1]:443: i/o timeout",
		"lookup vpn.example.com: no such host",
	} {
		out := scrub(s)
		if strings.Contains(out, "203.0") || strings.Contains(out, "2001:db8") || strings.Contains(out, "example.com") {
			t.Fatalf("%q -> %q", s, out)
		}
	}
}

func TestErrorClass(t *testing.T) {
	cases := map[string]error{
		"timeout":            context.DeadlineExceeded,
		"eof":                io.EOF,
		"refused":            &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")},
		"reset":              errors.New("read: connection reset by peer"),
		"tls:bad record mac": errors.New("remote error: tls: bad record mac"),
		"closed":             net.ErrClosed,
	}
	for want, err := range cases {
		if got := class(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
	if got := class(syscall.Errno(0)); got == "" {
		t.Error("an unknown errno still has a class")
	}
}

func TestRateLimitCountsTheRest(t *testing.T) {
	limiter.roll()
	for i := 0; i < perMinute+5; i++ {
		if ok := limiter.allow("x.test", "n"); ok != (i < perMinute) {
			t.Fatalf("record %d allowed=%v", i, ok)
		}
	}
	if s := limiter.roll(); s["x.test"] != 5 {
		t.Fatalf("suppressed %v", s)
	}
}

func end(chain string, up, down int64, life time.Duration, r RelayResult) TCPEnd {
	return TCPEnd{Dial: Dial{Network: "tcp", Host: "example.org", Port: 443}, Chain: []string{chain, "auto"},
		ID: "0123456789", Start: time.Now().Add(-life), Up: up, Down: down, State: &ConnState{}, Relay: r}
}

func TestSilentConnection(t *testing.T) {
	m := mark()
	now := time.Now()
	// the proxy closed after the request with nothing back
	TCPClosed(end("de", 517, 0, time.Second, RelayResult{DownAt: now, UpAt: now.Add(time.Millisecond), DownFirst: true}))
	// the client gave up early: not counted
	TCPClosed(end("de", 517, 0, time.Second, RelayResult{UpAt: now, DownAt: now.Add(time.Millisecond)}))
	// DIRECT is not journaled
	TCPClosed(end("DIRECT", 517, 0, time.Minute, RelayResult{DownAt: now, DownFirst: true}))
	got := collect(t, "tcp.silent", m)
	if len(got) != 1 || got[0]["node"] != "de" || got[0]["grp"] != "auto" || got[0]["dst"] != "example.org:443" {
		t.Fatalf("%v", got)
	}
}

func TestStallAndReset(t *testing.T) {
	m := mark()
	e := end("de", 100, 5000, 20*time.Second, RelayResult{})
	e.State.firstDown.Store(e.Start.Add(12 * time.Second).UnixNano())
	TCPClosed(e)

	now := time.Now()
	reset := errors.New("read: connection reset by peer")
	e = end("de", 100, 5000, time.Minute, RelayResult{DownErr: reset, DownRemote: true, DownAt: now, UpAt: now.Add(time.Millisecond), DownFirst: true})
	e.State.firstDown.Store(e.Start.Add(time.Second).UnixNano())
	e.State.lastDown.Store(now.Add(-40 * time.Second).UnixNano())
	e.State.lastUp.Store(now.Add(-time.Second).UnixNano())
	TCPClosed(e) // the client kept sending, nothing back for 40 s, then a reset: a stall

	e = end("de", 100, 5000, time.Minute, RelayResult{DownErr: reset, DownRemote: true, DownAt: now, UpAt: now.Add(time.Millisecond), DownFirst: true})
	e.State.firstDown.Store(e.Start.Add(time.Second).UnixNano())
	e.State.lastDown.Store(now.UnixNano())
	TCPClosed(e) // a reset mid-transfer

	e = end("de", 100, 5000, time.Minute, RelayResult{DownErr: reset, DownRemote: false, DownAt: now, UpAt: now.Add(time.Millisecond), DownFirst: true})
	TCPClosed(e) // the client's socket: not the proxy

	stalls := collect(t, "tcp.stall", m)
	if len(stalls) != 2 || stalls[0]["reason"] != "ttfb" || stalls[1]["reason"] != "gap" {
		t.Fatalf("stalls %v", stalls)
	}
	if resets := collect(t, "tcp.reset", m); len(resets) != 1 || resets[0]["class"] != "reset" {
		t.Fatalf("resets %v", resets)
	}
}

func TestDialStages(t *testing.T) {
	m := mark()
	ctx, tr := WithTrace(context.Background())
	Connected(ctx, time.Now(), errors.New("connect: connection refused"))
	tr.Failed(nil)
	DialFailed(tr, Dial{Proxy: "de", Network: "tcp"}, errors.New("connect: connection refused"))

	ctx, tr = WithTrace(context.Background())
	Connected(ctx, time.Now().Add(-40*time.Millisecond), nil)
	DialFailed(tr, Dial{Proxy: "de", Network: "tcp"}, errors.New("remote error: tls: handshake failure"))

	_, tr = WithTrace(context.Background())
	DialFailed(tr, Dial{Proxy: "hy", Network: "tcp"}, errors.New("timeout: no recent network activity"))

	ctx, tr = WithTrace(context.Background())
	ServerDNSFailed(ctx)
	DialFailed(tr, Dial{Proxy: "de", Network: "tcp"}, errors.New("no such host"))

	got := collect(t, "dial.fail", m)
	want := []string{"connect", "handshake", "session", "server_dns"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i, w := range want {
		if got[i]["stage"] != w {
			t.Errorf("record %d: stage %v, want %s", i, got[i]["stage"], w)
		}
	}
	if got[0]["attempts"] != float64(2) || got[1]["class"] != "tls:handshake failure" {
		t.Fatalf("%v", got)
	}
}

func TestDialSlowAgainstMedian(t *testing.T) {
	m := mark()
	for i := 0; i < 10; i++ {
		ctx, tr := WithTrace(context.Background())
		Connected(ctx, time.Now().Add(-50*time.Millisecond), nil)
		DialDone(tr, Dial{Network: "tcp"}, []string{"slowtest"})
	}
	ctx, tr := WithTrace(context.Background())
	Connected(ctx, time.Now().Add(-900*time.Millisecond), nil)
	DialDone(tr, Dial{Network: "tcp"}, []string{"slowtest"})
	if got := collect(t, "dial.slow", m); len(got) != 1 || got[0]["stage"] != "connect" {
		t.Fatalf("%v", got)
	}
}

func TestUDPOneWayDropsAndRebind(t *testing.T) {
	m := mark()
	conn, sender := &UDPState{}, &UDPState{}
	conn.SetIdle()
	sender.QueueDrop()
	sender.WriteErr(errors.New("write udp 203.0.113.7:443: message too long"))
	UDPClosed(UDPEnd{Dial: Dial{Network: "udp", Host: "example.org", Port: 443}, Chain: []string{"de"}, Key: "k1",
		Start: time.Now().Add(-time.Minute), Up: 1200, Idle: conn.IsIdle(), Conn: conn, Sender: sender})
	UDPStarted("k1", Dial{Proxy: "de", Host: "example.org", Port: 443})
	UDPStarted("k1", Dial{Proxy: "de"}) // only once
	if got := collect(t, "udp.oneway", m); len(got) != 1 {
		t.Fatalf("oneway %v", got)
	}
	drops := collect(t, "udp.drop", m)
	if len(drops) != 1 || drops[0]["queue_late"] != float64(1) || drops[0]["write_err"] != float64(1) ||
		strings.Contains(drops[0]["err"].(string), "203.0") {
		t.Fatalf("drops %v", drops)
	}
	if got := collect(t, "udp.rebind", m); len(got) != 1 || got[0]["idle_s"] != float64(60) {
		t.Fatalf("rebind %v", got)
	}
}

func TestDNSReasons(t *testing.T) {
	m := mark()
	DNS("https://dns.nextdns.io/abc123", "www.example.org.", "A", 10*time.Millisecond, nil, 2, false)
	DNS("8.8.8.8:53", "example.org.", "A", 10*time.Millisecond, nil, 0, true)
	DNS("8.8.8.8:53", "example.org.", "A", 10*time.Millisecond, context.Canceled, -1, false)
	DNS("8.8.8.8:53", "example.org.", "A", 10*time.Millisecond, nil, 0, false)
	got := collect(t, "dns.fail", m)
	if len(got) != 2 || got[0]["reason"] != "servfail" || got[1]["reason"] != "truncated" {
		t.Fatalf("%v", got)
	}
	if got[0]["node"] != "https nextdns.io" || got[0]["domain"] != "example.org" || got[1]["node"] != "udp 8.8.8.8" {
		t.Fatalf("names %v", got)
	}
}

func TestGroupSwitchAndFlap(t *testing.T) {
	m := mark()
	g, flappy := "g"+strconv.FormatInt(time.Now().UnixNano(), 10), "flappy"+strconv.FormatInt(time.Now().UnixNano(), 10)
	GroupPick(g, "a")
	GroupPick(g, "a")
	GroupPick(g, "b")
	for i := 0; i < 5; i++ {
		NodeAlive(flappy, i%2 == 0)
	}
	if got := collect(t, "group.switch", m); len(got) != 1 || got[0]["from"] != "a" || got[0]["to"] != "b" {
		t.Fatalf("switch %v", got)
	}
	if got := collect(t, "group.flap", m); len(got) != 1 {
		t.Fatalf("flap %v", got)
	}
}

// A client that half closes towards a proxy connection without CloseWrite loses the
// answer still on its way: the relay reports it.
func TestRelayReportsLostHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	inbound := <-accepted
	proxySide, server := net.Pipe() // no CloseWrite
	go func() {
		buf := make([]byte, 16)
		_, _ = server.Read(buf)
		time.Sleep(100 * time.Millisecond) // the answer comes after the client's FIN
		_, _ = server.Write([]byte("answer"))
	}()
	go func() {
		_, _ = client.Write([]byte("request"))
		_ = client.(*net.TCPConn).CloseWrite()
	}()
	r := Relay(inbound, proxySide)
	if !r.UpHalfLost || r.UpErr != nil || r.DownErr == nil {
		t.Fatalf("%+v", r)
	}
}
