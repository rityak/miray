//go:build unix

package inbound_test

import (
	"syscall"
	"testing"
	"time"
)

type cpuMark time.Duration

func processCPU() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func startCPU() cpuMark { return cpuMark(processCPU()) }

// report adds cpu-ns/op: user+sys of the whole process per op.
func (m cpuMark) report(b *testing.B) {
	b.ReportMetric(float64(processCPU()-time.Duration(m))/float64(b.N), "cpu-ns/op")
}
