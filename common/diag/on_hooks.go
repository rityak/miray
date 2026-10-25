//go:build miraydiag

package diag

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/quic-go"
)

// Thresholds of the single records (S-019).
const (
	slowFloor  = 500 * time.Millisecond // dial.slow, dns slow: and over 3 medians
	ttfbStall  = 10 * time.Second       // tcp.stall: no answer to a request this long
	gapStall   = 30 * time.Second       // tcp.stall: client sent, nothing back this long, then an error
	silentLife = 5 * time.Second        // tcp.silent: a client that gave up sooner is not counted
	rebindIn   = 5 * time.Minute        // udp.rebind: a flow back this soon after its idle close
	udpIdle    = 60 * time.Second       // tunnel.udpTimeout, for the record
	flapWindow = 10 * time.Minute       // group.flap: more than 3 alive changes in this window
)

var (
	lookupMu  sync.RWMutex
	protoFn   func(string) string
	connsFn   func() (int, int)
	protoMemo sync.Map // node → proto, cleared on a config
)

// SetLookups gives the journal the type of a proxy by name and the open connection counts.
func SetLookups(proto func(string) string, conns func() (int, int)) {
	lookupMu.Lock()
	protoFn, connsFn = proto, conns
	lookupMu.Unlock()
}

func protoOf(node string) string {
	if v, ok := protoMemo.Load(node); ok {
		return v.(string)
	}
	lookupMu.RLock()
	fn := protoFn
	lookupMu.RUnlock()
	p := ""
	if fn != nil {
		func() {
			defer func() { _ = recover() }() // the journal must never take the core down
			p = fn(node)
		}()
	}
	protoMemo.Store(node, p)
	return p
}

func connCount() (int, int) {
	lookupMu.RLock()
	fn := connsFn
	lookupMu.RUnlock()
	if fn == nil {
		return 0, 0
	}
	return fn()
}

// proxied is a node that is a proxy: DIRECT, REJECT and the like are not journaled.
func proxied(node string) bool {
	switch protoOf(node) {
	case "Direct", "Reject", "RejectDrop", "Compatible", "Pass", "Dns":
		return false
	}
	return node != ""
}

// node and group of a chain: the chain lists the proxy first and the outermost group last.
func nodeOf(chain []string) (node, group string) {
	if len(chain) == 0 {
		return "", ""
	}
	if len(chain) > 1 {
		group = chain[len(chain)-1]
	}
	return chain[0], group
}

// Configured records a config and opens the journal on the first one.
func Configured(c Config) {
	if !active {
		return
	}
	start(c)
	protoMemo.Range(func(k, _ any) bool { protoMemo.Delete(k); return true })
	event("config", "", "tun", c.TunEnable, "stack", c.TunStack, "dns", c.DNS, "sniffer", c.Sniffer,
		"proxies", c.Proxies, "groups", c.Groups, "udp_idle_s", int(udpIdle/time.Second))
}

// Trace follows the dial of one proxied connection through its context.
type Trace struct {
	start     time.Time
	connect   atomic.Int64 // first successful connect to a server, ns
	tried     atomic.Bool  // a new connection to a server was attempted
	dnsFailed atomic.Bool  // the server's name did not resolve
	failures  atomic.Int32 // failed attempts (retries)
}

type traceKey struct{}

func WithTrace(ctx context.Context) (context.Context, *Trace) {
	if !active {
		return ctx, nil
	}
	t := &Trace{start: time.Now()}
	return context.WithValue(ctx, traceKey{}, t), t
}

func traceOf(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

// ServerDNSFailed marks that the proxy server's name did not resolve.
func ServerDNSFailed(ctx context.Context) {
	if t := traceOf(ctx); t != nil {
		t.dnsFailed.Store(true)
	}
}

// Connected records the connect to a server that started at start (dialer.DialContext).
func Connected(ctx context.Context, start time.Time, err error) {
	if t := traceOf(ctx); t != nil {
		t.tried.Store(true)
		if err == nil {
			d := int64(time.Since(start))
			if d == 0 {
				d = 1 // a connect within one tick of the clock still counts
			}
			t.connect.CompareAndSwap(0, d)
		}
	}
}

// Failed counts a failed attempt; the tunnel retries it.
func (t *Trace) Failed(error) {
	if t != nil {
		t.failures.Add(1)
	}
}

// DialFailed records a connection that did not get through after all retries.
func DialFailed(t *Trace, d Dial, err error) {
	if t == nil || errors.Is(err, context.Canceled) {
		return
	}
	// handshake: the server was reached; connect: it was not; server_dns: its name did not
	// resolve; session: no new connection was needed (a QUIC or mux session of the proxy)
	connect := time.Duration(t.connect.Load())
	stage := "handshake"
	switch {
	case connect > 0:
	case t.dnsFailed.Load():
		stage = "server_dns"
	case t.tried.Load():
		stage = "connect"
	default:
		stage = "session"
	}
	summary.update(d.Proxy, func(n *nodeStats) { n.fail++ })
	event("dial.fail", d.Proxy, "proto", protoOf(d.Proxy), "net", d.Network, "stage", stage,
		"class", class(err), "err", err, "attempts", int(t.failures.Load())+1,
		"ms", time.Since(t.start), "connect_ms", connect, "dst", dst(d.Host, d.IP, d.Port),
		"rule", d.Rule, "in", d.Inbound)
}

// DialDone records the stage times of a connection that got through.
func DialDone(t *Trace, d Dial, chain []string) {
	if t == nil {
		return
	}
	node, group := nodeOf(chain)
	if !proxied(node) {
		return
	}
	total := time.Since(t.start)
	connect := time.Duration(t.connect.Load())
	handshake := total - connect
	failures := t.failures.Load()
	summary.update(node, func(n *nodeStats) {
		if d.Network == "udp" {
			n.udp++
		} else {
			n.tcp++
		}
		if failures > 0 {
			n.retried++
		}
		if connect > 0 {
			n.connect = sample(n.connect, ms(connect))
		}
		n.handshake = sample(n.handshake, ms(handshake))
	})
	var slowStage string
	if connect > 0 && slow(ms(connect), medians.add(node+"\x00connect", ms(connect)), ms(slowFloor)) {
		slowStage = "connect"
	}
	if slow(ms(handshake), medians.add(node+"\x00handshake", ms(handshake)), ms(slowFloor)) {
		slowStage = "handshake"
	}
	if slowStage != "" {
		event("dial.slow", node, "proto", protoOf(node), "grp", group, "stage", slowStage,
			"connect_ms", connect, "handshake_ms", handshake, "retries", int(failures),
			"dst", dst(d.Host, d.IP, d.Port))
	}
}

// Dropped records a connection dropped before a dial: no fake-ip record and nothing
// sniffed, invalid metadata, no rule result.
func Dropped(reason string, d Dial) {
	if !active {
		return
	}
	summary.mu.Lock()
	summary.dropped++
	summary.mu.Unlock()
	event("conn.dropped", "", "reason", reason, "net", d.Network, "in", d.Inbound,
		"dst", dst(d.Host, d.IP, d.Port))
}

// ConnState is the activity of a relayed TCP connection (unix ns).
type ConnState struct {
	firstDown, lastDown, lastUp atomic.Int64
}

func (s *ConnState) Up(n int64) {
	if n > 0 {
		s.lastUp.Store(time.Now().UnixNano())
	}
}

func (s *ConnState) Down(n int64) {
	if n <= 0 {
		return
	}
	now := time.Now().UnixNano()
	if s.firstDown.Load() == 0 {
		s.firstDown.CompareAndSwap(0, now)
	}
	s.lastDown.Store(now)
}

// TCPClosed decides what a finished TCP connection shows: tcp.silent, tcp.stall,
// tcp.reset, tcp.halfclose.
func TCPClosed(e TCPEnd) {
	node, group := nodeOf(e.Chain)
	if !active || !proxied(node) {
		return
	}
	now := time.Now()
	life := now.Sub(e.Start)
	var firstDown, lastDown, lastUp time.Time
	if e.State != nil {
		firstDown, lastDown, lastUp = unixTime(e.State.firstDown.Load()), unixTime(e.State.lastDown.Load()), unixTime(e.State.lastUp.Load())
	}
	summary.update(node, func(n *nodeStats) {
		n.up += e.Up
		n.down += e.Down
		if e.Up > 0 && !firstDown.IsZero() {
			n.ttfb = sample(n.ttfb, ms(firstDown.Sub(e.Start)))
		}
	})

	r := e.Relay
	downFirst := r.DownFirst
	endErr, endRemote, endDir := r.UpErr, r.UpRemote, "up"
	if downFirst {
		endErr, endRemote, endDir = r.DownErr, r.DownRemote, "down"
	}
	common := []any{"proto", protoOf(node), "grp", group, "conn", shortID(e.ID), "dst", dst(e.Host, e.IP, e.Port),
		"up", e.Up, "down", e.Down, "life_ms", life}
	closeKV := []any{"end", endDir, "class", class(endErr), "err", endErr, "remote", endRemote}

	switch {
	case e.Up > 0 && e.Down == 0 && (downFirst || life >= silentLife):
		event("tcp.silent", node, append(common, closeKV...)...)
	case e.Up > 0 && !firstDown.IsZero() && firstDown.Sub(e.Start) >= ttfbStall:
		event("tcp.stall", node, append(common, "reason", "ttfb", "ttfb_ms", firstDown.Sub(e.Start))...)
	case endErr != nil && endRemote && !lastDown.IsZero() && lastUp.After(lastDown) && now.Sub(lastDown) >= gapStall:
		event("tcp.stall", node, append(append(common, "reason", "gap", "gap_ms", now.Sub(lastDown)), closeKV...)...)
	case endErr != nil && endRemote && class(endErr) != "closed" && class(endErr) != "canceled":
		event("tcp.reset", node, append(common, closeKV...)...)
	}
	if r.UpHalfLost || r.DownHalfLost {
		dir := "up" // the client finished; the proxy connection had no CloseWrite
		if r.DownHalfLost {
			dir = "down"
		}
		event("tcp.halfclose", node, append(common, "finished", dir, "up_err", r.UpErr, "down_err", r.DownErr)...)
	}
}

func unixTime(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// UDPState counts the datagrams of a UDP session that never left.
type UDPState struct {
	queueDrops, fullDrops, writeErrs atomic.Int64
	firstErr                         atomic.Pointer[string]
	idle                             atomic.Bool
}

// SetIdle marks a session closed by its idle timer.
func (s *UDPState) SetIdle()     { s.idle.Store(true) }
func (s *UDPState) IsIdle() bool { return s.idle.Load() }

func (s *UDPState) QueueDrop() { s.queueDrops.Add(1) }
func (s *UDPState) FullDrop()  { s.fullDrops.Add(1) }

func (s *UDPState) WriteErr(err error) {
	s.writeErrs.Add(1)
	if s.firstErr.Load() == nil {
		e := scrub(err.Error())
		s.firstErr.CompareAndSwap(nil, &e)
	}
}

// idleClosed remembers flows closed by the idle timer (udp.rebind).
var idleClosed = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

// UDPStarted records a new session; one whose flow was closed idle shortly before is a
// rebind: the application kept its socket, the proxy gave it a new one.
func UDPStarted(key string, d Dial) {
	if !active {
		return
	}
	idleClosed.Lock()
	at, ok := idleClosed.m[key]
	delete(idleClosed.m, key)
	idleClosed.Unlock()
	if ok && time.Since(at) < rebindIn {
		event("udp.rebind", d.Proxy, "dst", dst(d.Host, d.IP, d.Port), "gap_s", int(time.Since(at)/time.Second),
			"idle_s", int(udpIdle/time.Second), "in", d.Inbound)
	}
}

func UDPClosed(e UDPEnd) {
	node, group := nodeOf(e.Chain)
	if !active || node == "" {
		return
	}
	if e.Idle {
		idleClosed.Lock()
		if len(idleClosed.m) > 4096 {
			for k, at := range idleClosed.m {
				if time.Since(at) > rebindIn {
					delete(idleClosed.m, k)
				}
			}
		}
		idleClosed.m[e.Key] = time.Now()
		idleClosed.Unlock()
	}
	if !proxied(node) {
		return
	}
	var queueDrops, fullDrops, writeErrs int64
	var firstErr string
	for _, s := range []*UDPState{e.Conn, e.Sender} {
		if s == nil {
			continue
		}
		queueDrops += s.queueDrops.Load()
		fullDrops += s.fullDrops.Load()
		writeErrs += s.writeErrs.Load()
		if p := s.firstErr.Load(); p != nil && firstErr == "" {
			firstErr = *p
		}
	}
	drops := queueDrops + fullDrops + writeErrs
	summary.update(node, func(n *nodeStats) {
		n.up += e.Up
		n.down += e.Down
		n.udpDrops += int(drops)
	})
	common := []any{"proto", protoOf(node), "grp", group, "dst", dst(e.Host, e.IP, e.Port),
		"up", e.Up, "down", e.Down, "life_ms", time.Since(e.Start), "idle", e.Idle}
	if e.Up > 0 && e.Down == 0 {
		event("udp.oneway", node, common...)
	}
	if drops > 0 {
		event("udp.drop", node, append(common, "queue_late", queueDrops, "queue_full", fullDrops,
			"write_err", writeErrs, "err", firstErr)...)
	}
}

// WatchQUIC records how a QUIC connection of a proxy ended, unless we closed it normally.
func WatchQUIC(node string, conn *quic.Conn, start time.Time) {
	if !active || conn == nil {
		return
	}
	go func() {
		<-conn.Context().Done()
		cause := context.Cause(conn.Context())
		st := conn.ConnectionStats()
		var reason, code string
		remote := false
		var (
			appErr       *quic.ApplicationError
			transportErr *quic.TransportError
			idleErr      *quic.IdleTimeoutError
			resetErr     *quic.StatelessResetError
		)
		switch {
		case errors.As(cause, &appErr):
			if !appErr.Remote && appErr.ErrorCode == 0 {
				return // our own close
			}
			reason, code, remote = "application", appErr.Error(), appErr.Remote
		case errors.As(cause, &transportErr):
			if !transportErr.Remote && transportErr.ErrorCode == quic.NoError {
				return
			}
			reason, code, remote = "transport", transportErr.ErrorCode.String(), transportErr.Remote
		case errors.As(cause, &idleErr):
			reason = "idle_timeout"
		case errors.As(cause, &resetErr):
			reason, remote = "stateless_reset", true
		case cause == nil || errors.Is(cause, context.Canceled):
			return
		default:
			reason, code = "other", scrub(cause.Error())
		}
		event("quic.close", node, "proto", protoOf(node), "reason", reason, "code", scrub(code), "remote", remote,
			"life_s", int(time.Since(start)/time.Second), "srtt_ms", round1(ms(st.SmoothedRTT)),
			"min_rtt_ms", round1(ms(st.MinRTT)), "rttvar_ms", round1(ms(st.MeanDeviation)),
			"sent_pkts", int64(st.PacketsSent), "lost_pkts", int64(st.PacketsLost),
			"lost_pct", round1(pct(int64(st.PacketsLost), int64(st.PacketsSent))),
			"sent", int64(st.BytesSent), "recv", int64(st.BytesReceived))
	}()
}

// well-known public resolvers keep their address in the journal; any other is hashed.
var publicResolvers = map[string]bool{
	"1.1.1.1": true, "1.0.0.1": true, "8.8.8.8": true, "8.8.4.4": true, "9.9.9.9": true,
	"149.112.112.112": true, "208.67.222.222": true, "77.88.8.8": true, "77.88.8.1": true,
	"223.5.5.5": true, "119.29.29.29": true, "94.140.14.14": true,
}

// upstream names a DNS server as scheme and host, without path or query (a DoH path
// can carry a personal id).
func upstream(addr string) string {
	scheme := "udp"
	if s, rest, ok := strings.Cut(addr, "://"); ok {
		scheme, addr = s, rest
	}
	if i := strings.IndexAny(addr, "/?#"); i >= 0 {
		addr = addr[:i]
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicResolvers[ip.String()] {
			host = ipName(ip)
		}
	} else if d := dst(host, netip.Addr{}, 0); d != "" {
		host = strings.TrimSuffix(d, ":0")
	}
	return scheme + " " + host
}

// DNS records an exchange with an upstream that failed, came back SERVFAIL/REFUSED or
// truncated (no retry over TCP, RFC 7766 §6.2.1), or was slow.
func DNS(addr, domain, qtype string, d time.Duration, err error, rcode int, truncated bool) {
	if !active || errors.Is(err, context.Canceled) {
		return
	}
	up := upstream(addr)
	name := dst(strings.TrimSuffix(domain, "."), netip.Addr{}, 0)
	name = strings.TrimSuffix(name, ":0")
	reason := ""
	switch {
	case err != nil:
		reason = class(err)
	case rcode == 2:
		reason = "servfail"
	case rcode == 5:
		reason = "refused"
	case truncated:
		reason = "truncated"
	}
	med := medians.add("dns\x00"+up, ms(d))
	if reason == "" && slow(ms(d), med, ms(slowFloor)) {
		reason = "slow"
	}
	if reason != "" {
		event("dns.fail", up, "reason", reason, "err", err, "domain", name, "qtype", qtype, "ms", d,
			"median_ms", round1(med))
	}
}

var groupLast sync.Map // group → node it used last

// GroupPick records the node a group (fallback, url-test) dials first; a change is a switch.
func GroupPick(group, node string) {
	if !active {
		return
	}
	if prev, loaded := groupLast.Swap(group, node); loaded && prev.(string) != node {
		event("group.switch", group, "from", prev.(string), "to", node)
	}
}

// GroupRace records a race the group's node lost to another without failing.
func GroupRace(group, first, winner string) {
	if !active {
		return
	}
	summary.update(first, func(n *nodeStats) { n.racesLost++ })
	event("group.race", group, "first", first, "winner", winner)
}

// GroupAside records a node set aside after another got through (T-129, T-135).
func GroupAside(group, failed, winner string, wholeServer bool, err error) {
	if !active {
		return
	}
	event("group.aside", group, "failed", failed, "winner", winner, "whole_server", wholeServer,
		"class", class(err), "err", err)
}

// GroupCheck records a health check a group started on failures or lost races.
func GroupCheck(group, reason string) {
	if active {
		event("group.check", group, "reason", reason)
	}
}

// GroupChecked records the verdict of that check.
func GroupChecked(group string, alive, total int) {
	if active {
		event("group.checked", group, "alive", alive, "total", total)
	}
}

var flaps = struct {
	sync.Mutex
	last    map[string]bool
	changes map[string][]time.Time
}{last: map[string]bool{}, changes: map[string][]time.Time{}}

// NodeAlive records a health check result of a node: more than 3 changes in 10 minutes
// is a flapping node.
func NodeAlive(node string, alive bool) {
	if !active {
		return
	}
	flaps.Lock()
	prev, seen := flaps.last[node]
	flaps.last[node] = alive
	if !seen || prev == alive {
		flaps.Unlock()
		return
	}
	now := time.Now()
	kept := flaps.changes[node][:0]
	for _, t := range flaps.changes[node] {
		if now.Sub(t) < flapWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	n := len(kept)
	if n > 3 {
		kept = kept[:0]
	}
	flaps.changes[node] = kept
	flaps.Unlock()
	if n > 3 {
		event("group.flap", node, "changes", n, "window_min", int(flapWindow/time.Minute), "alive", alive)
	}
}

// NetChange records a change of the default interface; "" is a lost interface.
func NetChange(iface string) {
	if active {
		event("net.change", "", "iface", iface, "lost", iface == "")
	}
}

// TunError counts an error of the TUN stack; the minute record keeps one sample.
func TunError(err error) {
	if !active || err == nil {
		return
	}
	summary.mu.Lock()
	summary.tunErrors++
	if summary.tunSample == "" {
		summary.tunSample = scrub(err.Error())
	}
	summary.mu.Unlock()
}
