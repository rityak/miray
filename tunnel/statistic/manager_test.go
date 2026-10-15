package statistic

import (
	"bytes"
	"encoding/json"
	"testing"
)

// T-013: every Read/Write of every connection pushes to the global counters.
func BenchmarkPushParallel(b *testing.B) {
	m := &Manager{}
	b.RunParallel(func(pb *testing.PB) {
		up := true
		for pb.Next() {
			if up {
				m.PushUploaded(1200)
			} else {
				m.PushDownloaded(1200)
			}
			up = !up
		}
	})
}

func TestManagerRate(t *testing.T) {
	m := &Manager{}
	var lastUp int64
	m.PushUploaded(100)
	m.PushUploaded(50)
	m.PushDownloaded(7)
	lastUp = m.tick(&m.uploadTotal, &m.uploadBlip, lastUp)
	m.tick(&m.downloadTotal, &m.downloadBlip, 0)
	if up, down := m.Now(); up != 150 || down != 7 {
		t.Fatalf("rate = %d/%d, want 150/7", up, down)
	}
	m.PushUploaded(30)
	lastUp = m.tick(&m.uploadTotal, &m.uploadBlip, lastUp)
	if up, _ := m.Now(); up != 30 {
		t.Fatalf("second rate = %d, want 30", up)
	}
	m.ResetStatistic()
	m.PushUploaded(10)
	m.tick(&m.uploadTotal, &m.uploadBlip, lastUp)
	if up, _ := m.Now(); up != 0 {
		t.Fatalf("rate after reset = %d, want 0", up)
	}
	if up, down := m.Total(); up != 10 || down != 0 {
		t.Fatalf("total after reset = %d/%d, want 10/0", up, down)
	}
}

// T-013: the upload and download relay goroutines of one connection count into
// the same TrackerInfo.
func BenchmarkTrackerInfoBothDirections(b *testing.B) {
	info := &TrackerInfo{}
	done := make(chan struct{})
	go func() {
		for i := 0; i < b.N; i++ {
			info.DownloadTotal.Add(1200)
		}
		close(done)
	}()
	for i := 0; i < b.N; i++ {
		info.UploadTotal.Add(1200)
	}
	<-done
}

// The cache-line padding must not appear in the /connections JSON.
func TestTrackerInfoJSONUnchanged(t *testing.T) {
	b, err := json.Marshal(&TrackerInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"_"`)) || !bytes.Contains(b, []byte(`"upload":0`)) || !bytes.Contains(b, []byte(`"download":0`)) {
		t.Fatalf("unexpected JSON: %s", b)
	}
}
