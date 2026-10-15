//go:build benchhandicap

package handicap

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/log"
)

const Enabled = true

// Config is parsed from MIRAY_HANDICAP, e.g.
// "spin=0400ns,copy=0,alloc=0,drop=0.000,hop=0,readcap=0,seed=1".
// Keep the string the same length in every arm of a comparison: the size of
// the environment shifts the stack (Mytkowicz et al.).
type Config struct {
	Spin    time.Duration // busy loop per packet
	Copy    bool          // memmove of the packet into a scratch buffer
	Alloc   bool          // heap copy of the packet (GC pressure)
	Drop    float64       // drop probability per packet
	Hop     bool          // extra goroutine round trip per packet
	ReadCap int           // relayed TCP reads are cut to this many bytes; 0 = no wrap
	Seed    uint64        // drop sequence seed
}

var (
	cfg       Config
	dropBound uint64 // drop when hash < dropBound
	dropSeq   atomic.Uint64
	scratch   = sync.Pool{New: func() any { b := make([]byte, 65536); return &b }}
	allocSink atomic.Pointer[[]byte]
)

func init() {
	c, err := Parse(os.Getenv("MIRAY_HANDICAP"))
	if err != nil {
		log.Fatalln("MIRAY_HANDICAP: %s", err)
	}
	set(c)
	log.Warnln("BENCH HANDICAP BUILD: %+v", c)
}

func set(c Config) {
	cfg = c
	dropBound = 0
	switch {
	case c.Drop >= 1:
		dropBound = math.MaxUint64
	case c.Drop > 0:
		dropBound = uint64(c.Drop * (1 << 63) * 2)
	}
	dropSeq.Store(0)
}

// Parse reads a comma-separated key=value list; empty means no handicap.
func Parse(s string) (Config, error) {
	var c Config
	for _, kv := range strings.Split(s, ",") {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return c, fmt.Errorf("%q: want key=value", kv)
		}
		var err error
		switch k {
		case "spin":
			c.Spin, err = time.ParseDuration(v)
		case "copy":
			c.Copy, err = parseBool(v)
		case "alloc":
			c.Alloc, err = parseBool(v)
		case "drop":
			c.Drop, err = strconv.ParseFloat(v, 64)
			if err == nil && (c.Drop < 0 || c.Drop > 1) {
				err = fmt.Errorf("out of [0,1]")
			}
		case "hop":
			c.Hop, err = parseBool(v)
		case "readcap":
			c.ReadCap, err = strconv.Atoi(v)
		case "seed":
			c.Seed, err = strconv.ParseUint(v, 10, 64)
		default:
			err = fmt.Errorf("unknown key")
		}
		if err != nil {
			return c, fmt.Errorf("%s: %w", kv, err)
		}
	}
	return c, nil
}

func parseBool(v string) (bool, error) {
	n, err := strconv.Atoi(v)
	return n != 0, err
}

func Packet(b []byte) bool {
	if cfg.Spin > 0 {
		for start := time.Now(); time.Since(start) < cfg.Spin; {
		}
	}
	if cfg.Copy {
		p := scratch.Get().(*[]byte)
		copy(*p, b)
		scratch.Put(p)
	}
	if cfg.Alloc {
		c := make([]byte, len(b))
		copy(c, b)
		allocSink.Store(&c)
	}
	if cfg.Hop {
		done := make(chan struct{})
		go close(done)
		<-done
	}
	return dropBound != 0 && splitmix(cfg.Seed+dropSeq.Add(1)) < dropBound
}

// splitmix64: the n-th drop decision depends only on seed and n.
func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func WrapConn(c net.Conn) net.Conn {
	if cfg.ReadCap <= 0 {
		return c
	}
	return &capConn{Conn: c, n: cfg.ReadCap}
}

// capConn hides the fast paths of the wrapped conn (splice, ReadWaiter), so the
// baseline for stream handicaps is readcap=65536 (W0), not the production build.
type capConn struct {
	net.Conn
	n int
}

func (c *capConn) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.Conn.Read(p)
}
