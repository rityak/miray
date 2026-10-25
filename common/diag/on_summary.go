//go:build miraydiag

package diag

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

const maxSamples = 256 // per node and metric a minute

// summary is the minute record per node: everything ordinary is only counted here.
var summary = &minuteStats{nodes: map[string]*nodeStats{}}

type minuteStats struct {
	mu        sync.Mutex
	nodes     map[string]*nodeStats
	tunErrors int
	tunSample string
	dropped   int // connections dropped before a dial
}

type nodeStats struct {
	tcp, udp, fail, retried, racesLost, udpDrops int
	up, down                                     int64
	connect, handshake, ttfb                     []float64
}

func (m *minuteStats) node(name string) *nodeStats {
	n := m.nodes[name]
	if n == nil {
		n = &nodeStats{}
		m.nodes[name] = n
	}
	return n
}

func (m *minuteStats) update(name string, f func(*nodeStats)) {
	m.mu.Lock()
	f(m.node(name))
	m.mu.Unlock()
}

func sample(s []float64, v float64) []float64 {
	if len(s) < maxSamples {
		return append(s, v)
	}
	return s
}

// percentiles returns p50 and p95, or nil without samples.
func percentiles(s []float64) []float64 {
	if len(s) == 0 {
		return nil
	}
	sort.Float64s(s)
	at := func(p float64) float64 { return s[int(p*float64(len(s)-1)+0.5)] }
	return []float64{round1(at(0.5)), round1(at(0.95))}
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// roll returns the lines of the minute that ended at now and starts a new one.
func (m *minuteStats) roll(now time.Time) [][]byte {
	m.mu.Lock()
	nodes, tunErrors, tunSample, dropped := m.nodes, m.tunErrors, m.tunSample, m.dropped
	m.nodes, m.tunErrors, m.tunSample, m.dropped = map[string]*nodeStats{}, 0, "", 0
	m.mu.Unlock()
	suppressed := limiter.roll()
	lostRecords := lost.Swap(0)

	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	var out [][]byte
	for _, name := range names {
		n := nodes[name]
		kv := []any{"proto", protoOf(name)}
		for _, c := range []struct {
			k string
			v int
		}{{"tcp", n.tcp}, {"udp", n.udp}, {"fail", n.fail}, {"retried", n.retried}, {"races_lost", n.racesLost}, {"udp_drops", n.udpDrops}} {
			if c.v != 0 {
				kv = append(kv, c.k, c.v)
			}
		}
		if n.up != 0 || n.down != 0 {
			kv = append(kv, "up", n.up, "down", n.down)
		}
		if p := percentiles(n.connect); p != nil {
			kv = append(kv, "connect_ms", p)
		}
		if p := percentiles(n.handshake); p != nil {
			kv = append(kv, "handshake_ms", p)
		}
		if p := percentiles(n.ttfb); p != nil {
			kv = append(kv, "ttfb_ms", p)
		}
		out = append(out, encode(now, "minute", name, kv))
	}
	if tunErrors != 0 || dropped != 0 || len(suppressed) != 0 || lostRecords != 0 {
		var kv []any
		if tunErrors != 0 {
			kv = append(kv, "tun_errors", tunErrors, "tun_error", tunSample)
		}
		if dropped != 0 {
			kv = append(kv, "conn_dropped", dropped)
		}
		if len(suppressed) != 0 {
			kv = append(kv, "suppressed", suppressed)
		}
		if lostRecords != 0 {
			kv = append(kv, "journal_lost", lostRecords)
		}
		out = append(out, encode(now, "minute", "", kv))
	}
	return out
}

// medians keeps the last connect and handshake times of each node (dial.slow) and the
// last exchange times of each DNS upstream.
var medians = &rings{m: map[string]*ring{}}

const ringSize = 32

type rings struct {
	mu sync.Mutex
	m  map[string]*ring
}

type ring struct {
	v [ringSize]float64
	n int
}

// add records v and returns the median before it, 0 while there are fewer than 8 samples.
func (r *rings) add(key string, v float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.m[key]
	if g == nil {
		g = &ring{}
		r.m[key] = g
	}
	var med float64
	if g.n >= 8 {
		k := g.n
		if k > ringSize {
			k = ringSize
		}
		s := make([]float64, k)
		copy(s, g.v[:k])
		sort.Float64s(s)
		med = s[k/2]
	}
	g.v[g.n%ringSize] = v
	g.n++
	return med
}

// slow is a value over 3 medians and over floor milliseconds.
func slow(v, med, floor float64) bool { return med > 0 && v > 3*med && v > floor }

// proc snapshots the process every 5 minutes; goroutines growing three intervals in a
// row while connections do not is a leak.
var proc = &procState{}

type procState struct {
	goroutines []int
	conns      []int
}

func (p *procState) snapshot(now time.Time) []byte {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	g := runtime.NumGoroutine()
	tcp, udp := connCount()
	p.goroutines = append(p.goroutines, g)
	p.conns = append(p.conns, tcp+udp)
	if len(p.goroutines) > 4 {
		p.goroutines, p.conns = p.goroutines[1:], p.conns[1:]
	}
	if k := len(p.goroutines); k == 4 {
		growing := true
		for i := 1; i < k; i++ {
			if p.goroutines[i] <= p.goroutines[i-1] || p.conns[i] > p.conns[i-1] {
				growing = false
			}
		}
		if growing && p.goroutines[3] > p.goroutines[0]*11/10 {
			event("proc.leak", "", "goroutines", p.goroutines, "conns", p.conns)
		}
	}
	return encode(now, "proc", "", []any{
		"goroutines", g, "heap_mb", int64(ms.HeapAlloc >> 20), "sys_mb", int64(ms.Sys >> 20),
		"tcp", tcp, "udp", udp, "gc", int64(ms.NumGC),
	})
}
