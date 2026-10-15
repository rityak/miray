//go:build benchhandicap

package handicap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	c, err := Parse("spin=0400ns,copy=1,alloc=0,drop=0.010,hop=1,readcap=4096,seed=7")
	require.NoError(t, err)
	require.Equal(t, Config{Spin: 400 * time.Nanosecond, Copy: true, Drop: 0.01, Hop: true, ReadCap: 4096, Seed: 7}, c)

	c, err = Parse("")
	require.NoError(t, err)
	require.Equal(t, Config{}, c)

	for _, bad := range []string{"spin", "drop=2", "nope=1", "copy=x"} {
		_, err = Parse(bad)
		require.Error(t, err, bad)
	}
}

func TestDropRateAndDeterminism(t *testing.T) {
	defer set(Config{})
	run := func() []bool {
		set(Config{Drop: 0.01, Seed: 42})
		out := make([]bool, 200000)
		for i := range out {
			out[i] = Packet(nil)
		}
		return out
	}
	a, b := run(), run()
	require.Equal(t, a, b, "same seed, same drop sequence")
	n := 0
	for _, d := range a {
		if d {
			n++
		}
	}
	// binomial sd at p=1%, n=2e5 is ~44; 5 sd
	require.InDelta(t, 2000, n, 225)
}

func TestSpinCost(t *testing.T) {
	defer set(Config{})
	set(Config{Spin: 20 * time.Microsecond})
	start := time.Now()
	for i := 0; i < 100; i++ {
		require.False(t, Packet(nil))
	}
	require.GreaterOrEqual(t, time.Since(start), 2*time.Millisecond)
}
