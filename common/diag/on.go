//go:build miraydiag

package diag

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const Enabled = true

// buildMark is searched for by tools/scripts/build.sh: a production build must not carry it.
const buildMark = "MIRAY DIAG BUILD"

const (
	fileLimit   = 16 << 20  // rotate a journal file at this size
	dirLimit    = 128 << 20 // keep the journal folder under this size
	perMinute   = 20        // single records of one kind and node a minute; the rest is counted
	queueLength = 4096
)

// A record goes to the events file or to the minute file.
const (
	toEvents = iota
	toMinutes
)

type record struct {
	file int
	line []byte
}

var (
	active  = true // MIRAY_DIAG=off turns the journal off at start
	dirSet  string // MIRAY_DIAG=dir=<folder>
	queue   = make(chan record, queueLength)
	started sync.Once
	salt    [16]byte
	run0    Config // home, version and build of the first config, for the file headers

	lost atomic.Int64 // records dropped on a full queue

	// sink replaces the files in tests
	sink func(file int, line []byte)
)

func init() {
	_, _ = rand.Read(salt[:])
	for _, opt := range strings.Split(os.Getenv("MIRAY_DIAG"), ",") {
		switch k, v, _ := strings.Cut(strings.TrimSpace(opt), "="); k {
		case "off", "0", "false":
			active = false
		case "dir":
			dirSet = v
		}
	}
}

// start opens the journal once the home folder is known (the first config).
func start(c Config) {
	started.Do(func() {
		run0 = c
		if sink != nil {
			go run(nil)
			return
		}
		dir := dirSet
		if dir == "" {
			dir = filepath.Join(c.Home, "miray-diag")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			active = false
			return
		}
		go run(&files{dir: dir, stamp: time.Now().Format("20060102-150405")})
	})
}

// emit sends a record without ever blocking the caller.
func emit(file int, line []byte) {
	select {
	case queue <- record{file, line}:
	default:
		lost.Add(1)
	}
}

// event writes a single record unless kind and node already had perMinute this minute.
func event(kind, node string, kv ...any) {
	if !active || !limiter.allow(kind, node) {
		return
	}
	emit(toEvents, encode(time.Now(), kind, node, kv))
}

func encode(t time.Time, kind, node string, kv []any) []byte {
	b := make([]byte, 0, 256)
	b = append(b, `{"t":"`...)
	b = t.UTC().AppendFormat(b, "2006-01-02T15:04:05.000Z")
	b = append(b, `","k":`...)
	b = appendValue(b, kind)
	if node != "" {
		b = append(b, `,"node":`...)
		b = appendValue(b, node)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		switch x := v.(type) {
		case nil:
			continue
		case string:
			if x == "" {
				continue
			}
		case error:
			if x == nil {
				continue
			}
		}
		b = append(b, ',', '"')
		b = append(b, kv[i].(string)...)
		b = append(b, '"', ':')
		b = appendValue(b, v)
	}
	return append(b, '}', '\n')
}

func appendValue(b []byte, v any) []byte {
	switch x := v.(type) {
	case time.Duration: // milliseconds
		return strconv.AppendFloat(b, float64(x)/float64(time.Millisecond), 'f', 1, 64)
	case int:
		return strconv.AppendInt(b, int64(x), 10)
	case int64:
		return strconv.AppendInt(b, x, 10)
	case bool:
		return strconv.AppendBool(b, x)
	case float64:
		return strconv.AppendFloat(b, x, 'f', 1, 64)
	case error:
		v = scrub(x.Error())
	}
	j, err := json.Marshal(v)
	if err != nil {
		return append(b, `null`...)
	}
	return append(b, j...)
}

// limiter keeps single records of one kind and node to perMinute a minute.
var limiter = &rateLimit{seen: map[string]int{}, suppressed: map[string]int{}}

type rateLimit struct {
	mu         sync.Mutex
	seen       map[string]int
	suppressed map[string]int
}

func (r *rateLimit) allow(kind, node string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := kind + "\x00" + node
	if r.seen[key]++; r.seen[key] <= perMinute {
		return true
	}
	r.suppressed[kind]++
	return false
}

// roll starts a new minute and returns the records suppressed in the last one.
func (r *rateLimit) roll() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.suppressed
	r.seen, r.suppressed = map[string]int{}, map[string]int{}
	return out
}

type files struct {
	dir, stamp string
	events     *journalFile
	minutes    *journalFile
}

type journalFile struct {
	f    *os.File
	w    *bufio.Writer
	size int64
}

func (fs *files) write(file int, line []byte) {
	jf, prefix := &fs.events, "events"
	if file == toMinutes {
		jf, prefix = &fs.minutes, "minute"
	}
	if *jf != nil && (*jf).size+int64(len(line)) > fileLimit {
		(*jf).close()
		*jf = nil
		fs.stamp = time.Now().Format("20060102-150405")
	}
	if *jf == nil {
		name := filepath.Join(fs.dir, prefix+"-"+fs.stamp+".jsonl")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		*jf = &journalFile{f: f, w: bufio.NewWriterSize(f, 64<<10)}
		fs.prune()
		hdr := header()
		_, _ = (*jf).w.Write(hdr)
		(*jf).size += int64(len(hdr))
	}
	_, _ = (*jf).w.Write(line)
	(*jf).size += int64(len(line))
}

func (fs *files) flush() {
	for _, jf := range []*journalFile{fs.events, fs.minutes} {
		if jf != nil {
			_ = jf.w.Flush()
		}
	}
}

func (jf *journalFile) close() {
	_ = jf.w.Flush()
	_ = jf.f.Close()
}

// prune deletes the oldest journal files while the folder is over dirLimit.
func (fs *files) prune() {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		size int64
		mod  time.Time
	}
	var all []file
	var total int64
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			all = append(all, file{e.Name(), info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	for _, f := range all {
		if total <= dirLimit-2*fileLimit {
			return
		}
		if os.Remove(filepath.Join(fs.dir, f.name)) == nil {
			total -= f.size
		}
	}
}

func header() []byte {
	return encode(time.Now(), "start", "", []any{
		"version", run0.Version, "built", run0.Built, "os", runtime.GOOS, "arch", runtime.GOARCH,
		"go", runtime.Version(), "pid", os.Getpid(), "build", buildMark,
	})
}

// run writes the journal and, each minute, the summary; every 5 minutes the process snapshot.
func run(fs *files) {
	write := func(file int, line []byte) {
		if sink != nil {
			sink(file, line)
		} else if fs != nil {
			fs.write(file, line)
		}
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	flush := time.NewTicker(5 * time.Second)
	defer flush.Stop()
	minutes := 0
	for {
		select {
		case r := <-queue:
			write(r.file, r.line)
		case <-flush.C:
			if fs != nil {
				fs.flush()
			}
		case now := <-tick.C:
			for _, line := range summary.roll(now) {
				write(toMinutes, line)
			}
			if minutes++; minutes%5 == 0 {
				write(toMinutes, proc.snapshot(now))
			}
		}
	}
}

// hashIP names an address without revealing it: the same address gives the same name
// within a run (a random salt per run).
func hashIP(s string) string {
	h := sha256.New()
	h.Write(salt[:])
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil)[:3])
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func pct(n, of int64) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}
