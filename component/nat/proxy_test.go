package nat

import (
	"net"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

type sourcedPacket struct {
	C.UDPPacket
	src any
	id  int
}

func (p *sourcedPacket) WriteBack([]byte, net.Addr) (int, error) { return p.id, nil }
func (p *sourcedPacket) WriteBackSource() any                    { return p.src }

// The datagrams of one packet connection write back through one writer: the proxy keeps
// the writer it has instead of storing every datagram's (an allocation each, T-128); a
// datagram of another writer, or one without a source, still takes over.
func TestWriteBackProxyKeepsSameSource(t *testing.T) {
	a, b := new(int), new(int)
	w := NewWriteBackProxy(&sourcedPacket{src: a, id: 1})
	w.UpdateWriteBack(&sourcedPacket{src: a, id: 2})
	if n, _ := w.WriteBack(nil, nil); n != 1 {
		t.Fatalf("same source replaced the writer (got %d)", n)
	}
	w.UpdateWriteBack(&sourcedPacket{src: b, id: 3})
	if n, _ := w.WriteBack(nil, nil); n != 3 {
		t.Fatalf("another source did not take over (got %d)", n)
	}
	w.UpdateWriteBack(&sourcedPacket{src: nil, id: 4})
	if n, _ := w.WriteBack(nil, nil); n != 4 {
		t.Fatalf("a datagram without a source did not take over (got %d)", n)
	}
	w.UpdateWriteBack(&sourcedPacket{src: nil, id: 5})
	if n, _ := w.WriteBack(nil, nil); n != 5 {
		t.Fatalf("two datagrams without a source count as one writer (got %d)", n)
	}
	same := &sourcedPacket{src: b, id: 6}
	w.UpdateWriteBack(same)
	if allocs := testing.AllocsPerRun(100, func() { w.UpdateWriteBack(same) }); allocs != 0 {
		t.Fatalf("%.0f allocations per update of the same writer", allocs)
	}
}
