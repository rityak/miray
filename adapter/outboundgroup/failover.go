package outboundgroup

import (
	"context"
	"errors"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
)

// Failover of url-test, fallback and load-balance (T-129, user decision; RFC 8305: try
// the alternative quickly). A dial that fails on its node is made once more through the
// next usable node of the group: nothing has been sent yet, so the application sees one
// connection. If the next node gets through, the target is reachable and the first node
// is to blame: the group sets it aside for new connections at once (as sing-box drops
// the node's test result on a failed dial) and runs a health check. If the next node
// fails too, the target is the likely cause, nothing is set aside and the failure counts
// as before. Two attempts at most, so a dead target costs one extra dial, not the group.

// downFor bounds how long a node stays aside without a health check verdict.
const downFor = 30 * time.Second

type downNodes struct {
	mu    sync.Mutex
	until map[C.Proxy]time.Time
}

func (d *downNodes) set(p C.Proxy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.until == nil {
		d.until = make(map[C.Proxy]time.Time)
	}
	d.until[p] = time.Now().Add(downFor)
}

func (d *downNodes) any() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.until) > 0
}

func (d *downNodes) has(p C.Proxy) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.until[p]
	if ok && time.Now().After(until) {
		delete(d.until, p)
		return false
	}
	return ok
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

// nextUsable is the first node in order that is alive, not set aside and not skip.
func (gb *GroupBase) nextUsable(proxies []C.Proxy, testUrl string, skip C.Proxy) C.Proxy {
	for _, p := range proxies {
		if p != skip && p.AliveForTestUrl(testUrl) && !gb.down.has(p) {
			return p
		}
	}
	return nil
}

// noFailover are the adapters whose failure says nothing about a proxy node.
func noFailover(t C.AdapterType) bool {
	switch t {
	case C.Direct, C.Compatible, C.Reject, C.Pass, C.RejectDrop:
		return true
	}
	return false
}

// failover dials first and, when it fails before the application has sent anything,
// once more through next(first); see above. reset drops a cached choice of the group,
// healthCheck runs when a node is set aside.
func failover[T any](ctx context.Context, gb *GroupBase, first C.Proxy, next func(skip C.Proxy) C.Proxy,
	dial func(C.Proxy) (T, error), reset func(), healthCheck func()) (T, C.Proxy, error) {
	c, err := dial(first)
	if err == nil || ctx.Err() != nil || noFailover(first.Type()) || errors.Is(err, C.ErrNotSupport) {
		return c, first, err
	}
	second := next(first)
	if second == nil {
		return c, first, err
	}
	c2, err2 := dial(second)
	if err2 != nil {
		return c, first, err // both failed: likely the target, the first error stands
	}
	log.Warnln("ProxyGroup: %s: %s failed (%v), %s got through; %s set aside, health check", gb.Name(), first.Name(), err, second.Name(), first.Name())
	gb.down.set(first)
	if reset != nil {
		reset()
	}
	go healthCheck()
	return c2, second, nil
}
