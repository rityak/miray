package inbound_test

import (
	"syscall"
	"testing"
	"time"
)

type cpuMark time.Duration

func processCPU() time.Duration {
	var c, e, k, u syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	_ = syscall.GetProcessTimes(h, &c, &e, &k, &u)
	return ticks(k) + ticks(u)
}

// Filetime.Nanoseconds is for dates (it subtracts the 1601 epoch); these are durations in 100 ns.
func ticks(f syscall.Filetime) time.Duration {
	return time.Duration(int64(f.HighDateTime)<<32|int64(f.LowDateTime)) * 100
}

func startCPU() cpuMark { return cpuMark(processCPU()) }

// report adds cpu-ns/op: user+sys of the whole process per op. Windows counts
// CPU in 15.6 ms ticks: short runs read 0, and CPU is compared on Linux anyway.
func (m cpuMark) report(b *testing.B) {
	b.ReportMetric(float64(processCPU()-time.Duration(m))/float64(b.N), "cpu-ns/op")
}
