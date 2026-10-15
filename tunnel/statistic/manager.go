package statistic

import (
	"os"
	"time"

	"github.com/metacubex/mihomo/common/atomic"
	"github.com/metacubex/mihomo/common/xsync"
	"github.com/metacubex/mihomo/component/memory"
)

var DefaultManager *Manager

func init() {
	DefaultManager = &Manager{
		pid: int32(os.Getpid()),
	}

	go DefaultManager.handle()
}

type Manager struct {
	connections xsync.Map[string, Tracker]
	// Every Read/Write of every connection adds to these two counters (T-013):
	// one atomic per call, upload and download on separate cache lines so the
	// two relay directions do not contend. The per-second rate is derived from
	// them in handle.
	uploadTotal   atomic.Int64
	_             [56]byte
	downloadTotal atomic.Int64
	_             [56]byte
	uploadBlip    atomic.Int64
	downloadBlip  atomic.Int64
	pid           int32
	memory        uint64
}

func (m *Manager) Join(c Tracker) {
	m.connections.Store(c.ID(), c)
}

func (m *Manager) Leave(c Tracker) {
	m.connections.Delete(c.ID())
}

func (m *Manager) Get(id string) (c Tracker) {
	if value, ok := m.connections.Load(id); ok {
		c = value
	}
	return
}

func (m *Manager) Range(f func(c Tracker) bool) {
	m.connections.Range(func(key string, value Tracker) bool {
		return f(value)
	})
}

func (m *Manager) PushUploaded(size int64) {
	m.uploadTotal.Add(size)
}

func (m *Manager) PushDownloaded(size int64) {
	m.downloadTotal.Add(size)
}

func (m *Manager) Now() (up int64, down int64) {
	return m.uploadBlip.Load(), m.downloadBlip.Load()
}

func (m *Manager) Total() (up, down int64) {
	return m.uploadTotal.Load(), m.downloadTotal.Load()
}

func (m *Manager) Memory() uint64 {
	m.updateMemory()
	return m.memory
}

func (m *Manager) Snapshot() *Snapshot {
	var connections []*TrackerInfo
	m.Range(func(c Tracker) bool {
		connections = append(connections, c.Info())
		return true
	})
	return &Snapshot{
		UploadTotal:   m.uploadTotal.Load(),
		DownloadTotal: m.downloadTotal.Load(),
		Connections:   connections,
		Memory:        m.memory,
	}
}

func (m *Manager) updateMemory() {
	stat, err := memory.GetMemoryInfo(m.pid)
	if err != nil {
		return
	}
	m.memory = stat.RSS
}

func (m *Manager) ResetStatistic() {
	m.uploadBlip.Store(0)
	m.uploadTotal.Store(0)
	m.downloadBlip.Store(0)
	m.downloadTotal.Store(0)
}

func (m *Manager) handle() {
	ticker := time.NewTicker(time.Second)

	var lastUp, lastDown int64
	for range ticker.C {
		lastUp = m.tick(&m.uploadTotal, &m.uploadBlip, lastUp)
		lastDown = m.tick(&m.downloadTotal, &m.downloadBlip, lastDown)
	}
}

// tick stores the bytes counted since last as the per-second rate. After a
// ResetStatistic the total may be below last: that second reports zero.
func (m *Manager) tick(total, blip *atomic.Int64, last int64) int64 {
	now := total.Load()
	if rate := now - last; rate > 0 {
		blip.Store(rate)
	} else {
		blip.Store(0)
	}
	return now
}

type Snapshot struct {
	DownloadTotal int64          `json:"downloadTotal"`
	UploadTotal   int64          `json:"uploadTotal"`
	Connections   []*TrackerInfo `json:"connections"`
	Memory        uint64         `json:"memory"`
}
