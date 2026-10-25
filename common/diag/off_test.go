//go:build !miraydiag

package diag

import (
	"testing"
	"unsafe"
)

// A production build must carry no journal: hooks are off and their state takes no room.
func TestOffBuildIsNoop(t *testing.T) {
	if Enabled {
		t.Fatal("diag enabled without the miraydiag tag")
	}
	if unsafe.Sizeof(ConnState{}) != 0 || unsafe.Sizeof(UDPState{}) != 0 || unsafe.Sizeof(Trace{}) != 0 {
		t.Fatal("diag state takes room in a production build")
	}
}
