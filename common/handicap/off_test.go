//go:build !benchhandicap

package handicap

import "testing"

// A production build must not carry a handicap.
func TestOffBuildIsNoop(t *testing.T) {
	if Enabled || Packet(make([]byte, 1500)) {
		t.Fatal("handicap active without the benchhandicap tag")
	}
}
