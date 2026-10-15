//go:build benchhandicap

package handicap

import (
	"testing"
	"time"
)

// BenchmarkSpin2us: the cost of one Packet call at spin=2us must be ~2000 ns/op,
// otherwise doses do not mean what they say (VALID-05).
func BenchmarkSpin2us(b *testing.B) {
	defer set(Config{})
	set(Config{Spin: 2 * time.Microsecond})
	buf := make([]byte, 1200)
	for i := 0; i < b.N; i++ {
		Packet(buf)
	}
}
