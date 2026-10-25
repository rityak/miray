package outboundgroup

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/common/diag"
	"github.com/metacubex/mihomo/component/iface"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	"github.com/metacubex/quic-go"
)

// Failover of url-test, fallback and load-balance (T-129, T-135; user decisions; design
// and standards in docs/spikes/S-018).
//
// A dial races its node as Happy Eyeballs does (RFC 8305 §5): if the node has not
// connected within its attempt delay, the next candidate starts in parallel and the first
// to connect serves this connection; the other is canceled. Losing a race is not a
// failure: a node that is only slower than usual (jitter, a lagging network) is not set
// aside and the group's choice does not change, so the group never flaps on latency.
//
// A node that returns an error is classified by the stage it failed at:
//   - its server was not reached (connect timeout, no route, no answer to a QUIC
//     handshake): the retry prefers a node on another server IP; once a node on another
//     IP got through, every node of the failed IP is set aside (the server is down). A
//     sibling protocol on the same IP is still tried when the group has nothing else: a
//     SYN dropped on one port looks the same and the server may well be alive; when the
//     sibling gets through, only the failed node is set aside;
//   - its server answered (refused port, TLS or protocol error): only this endpoint is
//     broken, the retry may go to a sibling protocol on the same server, and only the node
//     is set aside.
// A node is set aside only on proof: another node got through after it failed. If both
// fail, the target is the likely cause and nothing is set aside.
//
// A dead server often loses the race before its own error comes (a SYN is retried for
// seconds): when the group's node loses max-failed-times races in a row, the group runs
// its health check and the verdict decides, as for dial failures upstream. Jitter alone
// does not lose races in a row, and a check that finds the node alive changes nothing.
//
// A node set aside stays out of the group's choice until the health check that its
// failure starts has finished (its verdict decides), until the network changes or the
// config is reloaded (iface.Generation), or for downFor at most. The alternatives come
// from next(), which changes no state of the group: fallback keeps its order, url-test
// its fastest node and tolerance, load-balance its hash, sticky and round-robin state.

// downFor bounds how long a node stays aside when no health check verdict comes.
const downFor = 30 * time.Second

// Attempt delay bounds (RFC 8305 §5: 250 ms recommended, 100 ms minimum, 2 s maximum);
// replaced in tests.
var (
	raceDelayMin     = 250 * time.Millisecond
	raceDelayMax     = 2 * time.Second
	raceDelayDefault = time.Second // a node without delay history
)

type downEntry struct {
	at, until time.Time
	gen       uint64
}

type downNodes struct {
	mu    sync.Mutex
	nodes map[C.Proxy]downEntry
}

func (d *downNodes) set(p C.Proxy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.nodes == nil {
		d.nodes = make(map[C.Proxy]downEntry)
	}
	now := time.Now()
	d.nodes[p] = downEntry{at: now, until: now.Add(downFor), gen: iface.Generation()}
}

func (d *downNodes) any() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.nodes) > 0
}

func (d *downNodes) has(p C.Proxy) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.nodes[p]
	if ok && (time.Now().After(e.until) || e.gen != iface.Generation()) {
		delete(d.nodes, p)
		return false
	}
	return ok
}

// settled drops the nodes set aside before a health check that has now finished: its
// verdict (the alive flags) decides from here.
func (d *downNodes) settled(checkStart time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for p, e := range d.nodes {
		if !e.at.After(checkStart) {
			delete(d.nodes, p)
		}
	}
}

// candidates is proxies less the nodes set aside; all of them when every one is.
func (gb *GroupBase) candidates(proxies []C.Proxy) []C.Proxy {
	if !gb.down.any() {
		return proxies
	}
	out := make([]C.Proxy, 0, len(proxies))
	for _, p := range proxies {
		if !gb.down.has(p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return proxies
	}
	return out
}

// usable are proxies in order that are alive, not set aside and not skip.
func (gb *GroupBase) usable(proxies []C.Proxy, testUrl string, skip C.Proxy) []C.Proxy {
	var out []C.Proxy
	for _, p := range proxies {
		if p != skip && p.AliveForTestUrl(testUrl) && !gb.down.has(p) {
			out = append(out, p)
		}
	}
	return out
}

// noFailover are the adapters whose failure says nothing about a proxy node.
func noFailover(t C.AdapterType) bool {
	switch t {
	case C.Direct, C.Compatible, C.Reject, C.Pass, C.RejectDrop:
		return true
	}
	return false
}

// hostUnreached tells a failure to reach the node's server at all from a failure of the
// endpoint on a server that answered (RFC 9293 RST, RFC 1122 ICMP unreachable).
func hostUnreached(err error) bool {
	var idle *quic.IdleTimeoutError
	var hs *quic.HandshakeTimeoutError
	if errors.As(err, &idle) || errors.As(err, &hs) {
		return true // no answer to the QUIC handshake
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		if op.Timeout() {
			return true // no answer to the SYN
		}
		s := op.Err.Error()
		return strings.Contains(s, "no route to host") || strings.Contains(s, "host is unreachable") ||
			strings.Contains(s, "network is unreachable") || strings.Contains(s, "host is down")
	}
	return false
}

// serverIP is the address a node dials, from the resolver's cache for a domain; invalid
// for a nested group or an unresolved name. Only the failure path asks.
func serverIP(p C.Proxy) netip.Addr {
	host, _, err := net.SplitHostPort(p.Addr())
	if err != nil || host == "" {
		return netip.Addr{}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := resolver.ProxyServerHostResolver
	var ips []netip.Addr
	if r != nil {
		ips, err = resolver.LookupIPWithResolver(ctx, host, r)
	} else {
		ips, err = resolver.LookupIP(ctx, host)
	}
	if err != nil || len(ips) == 0 {
		return netip.Addr{}
	}
	return ips[0].Unmap()
}

// raceDelay is the node's attempt delay: twice the worst of its recent check delays, so
// usual jitter does not start a race (RFC 8305 §5 allows a delay from RTT history).
func raceDelay(p C.Proxy, testUrl string) time.Duration {
	var worst uint16
	if state, ok := p.ExtraDelayHistories()[testUrl]; ok {
		for _, h := range state.History {
			if h.Delay > worst {
				worst = h.Delay
			}
		}
	}
	if worst == 0 {
		return raceDelayDefault
	}
	d := 2 * time.Duration(worst) * time.Millisecond
	if d < raceDelayMin {
		return raceDelayMin
	}
	if d > raceDelayMax {
		return raceDelayMax
	}
	return d
}

// pick is the first of alts on another server than avoid, else the first of alts.
func pick(alts []C.Proxy, avoid netip.Addr) C.Proxy {
	if len(alts) == 0 {
		return nil
	}
	if avoid.IsValid() {
		for _, p := range alts {
			if serverIP(p) != avoid {
				return p
			}
		}
	}
	return alts[0]
}

// setAside sets failed aside after another node got through; a server that was not
// reached takes every node of its IP in the group with it.
func (gb *GroupBase) setAside(failed C.Proxy, ip netip.Addr, wholeServer bool, members []C.Proxy) {
	gb.down.set(failed)
	if !wholeServer || !ip.IsValid() {
		return
	}
	for _, p := range members {
		if p != failed && serverIP(p) == ip {
			gb.down.set(p)
		}
	}
}

type attempt[T io.Closer] struct {
	conn T
	err  error
	node C.Proxy
}

// failover dials first, races the next candidate after first's attempt delay and retries
// on a failure, as described above. next lists the alternatives in the group's order of
// preference without changing its state; reset drops a cached choice of the group.
func failover[T io.Closer](ctx context.Context, gb *GroupBase, first C.Proxy, testUrl string,
	next func(skip C.Proxy) []C.Proxy, dial func(context.Context, C.Proxy) (T, error),
	reset func(), healthCheck func()) (T, C.Proxy, error) {
	if diag.Enabled && gb.Type() != C.LoadBalance {
		diag.GroupPick(gb.Name(), first.Name())
	}
	if noFailover(first.Type()) {
		c, err := dial(ctx, first)
		return c, first, err
	}

	results := make(chan attempt[T], 2)
	start := func(p C.Proxy) context.CancelFunc {
		actx, cancel := context.WithCancel(ctx)
		go func() {
			c, err := dial(actx, p)
			results <- attempt[T]{c, err, p}
		}()
		return cancel
	}
	cancels := map[C.Proxy]context.CancelFunc{first: start(first)}
	pending := 1
	timer := time.NewTimer(raceDelay(first, testUrl))
	defer timer.Stop()

	var second C.Proxy
	var firstErr error
	firstIP := netip.Addr{}
	for {
		select {
		case <-timer.C:
			if second != nil || firstErr != nil {
				continue
			}
			// first is slow: race the next candidate, another server if there is one
			firstIP = serverIP(first)
			if second = pick(next(first), firstIP); second != nil {
				cancels[second] = start(second)
				pending++
			}
		case r := <-results:
			pending--
			if r.err == nil {
				for p, cancel := range cancels {
					if p != r.node {
						cancel()
					}
				}
				if pending > 0 {
					go closeLate(results, pending)
				}
				if diag.Enabled && r.node != first && firstErr == nil {
					diag.GroupRace(gb.Name(), first.Name(), r.node.Name())
				}
				if r.node == first {
					gb.lostRaces.Store(0)
				} else if firstErr == nil && gb.lostRaces.Add(1) >= int32(gb.maxFailedTimes) {
					gb.lostRaces.Store(0)
					log.Infoln("ProxyGroup: %s: %s lost %d races in a row, health check", gb.Name(), first.Name(), gb.maxFailedTimes)
					if diag.Enabled {
						diag.GroupCheck(gb.Name(), "lost_races")
					}
					go healthCheck()
				}
				if r.node != first && firstErr != nil {
					// proof: first failed and another node got through
					wholeServer := hostUnreached(firstErr) && firstIP.IsValid() && serverIP(r.node) != firstIP
					log.Warnln("ProxyGroup: %s: %s failed (%v), %s got through; set aside (whole server: %v), health check",
						gb.Name(), first.Name(), firstErr, r.node.Name(), wholeServer)
					gb.setAside(first, firstIP, wholeServer, gb.GetProxies(false))
					if diag.Enabled {
						diag.GroupAside(gb.Name(), first.Name(), r.node.Name(), wholeServer, firstErr)
						diag.GroupCheck(gb.Name(), "set_aside")
					}
					if reset != nil {
						reset()
					}
					go healthCheck()
				}
				return r.conn, r.node, nil
			}
			if r.node == first {
				firstErr = r.err
				if ctx.Err() != nil || errors.Is(r.err, C.ErrNotSupport) {
					if pending > 0 {
						for _, cancel := range cancels {
							cancel()
						}
						go closeLate(results, pending)
					}
					var zero T
					return zero, first, r.err
				}
				if second == nil {
					// first failed before its delay: retry now
					firstIP = serverIP(first)
					second = pick(next(first), firstIP)
					if second == nil {
						var zero T
						return zero, first, r.err // nowhere else: up to the outer group
					}
					cancels[second] = start(second)
					pending++
				}
			}
			if pending == 0 {
				var zero T
				if firstErr == nil {
					firstErr = r.err
				}
				return zero, first, firstErr // all failed: likely the target, nothing set aside
			}
		case <-ctx.Done():
			for _, cancel := range cancels {
				cancel()
			}
			go closeLate(results, pending)
			var zero T
			return zero, first, ctx.Err()
		}
	}
}

// closeLate closes the connections of attempts that finish after the race was decided.
func closeLate[T io.Closer](results chan attempt[T], pending int) {
	for ; pending > 0; pending-- {
		if r := <-results; r.err == nil {
			_ = r.conn.Close()
		}
	}
}
