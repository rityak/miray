package v5

import (
	"bytes"
	"net/netip"
	"reflect"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

// T-079: a packet written field by field reads back the same, a cut one fails.
func TestPacketRoundTrip(t *testing.T) {
	for _, addr := range []Address{
		NewAddressAddrPort(netip.MustParseAddrPort("192.0.2.1:443")),
		NewAddressAddrPort(netip.MustParseAddrPort("[2001:db8::1]:53")),
		NewAddress(&C.Metadata{Host: "example.com", DstPort: 8080}),
		{TYPE: AtypNone}, // a non-first fragment
	} {
		in := NewPacket(0x0102, 0x0304, 3, 1, 7, addr, []byte("payload"))
		var b bytes.Buffer
		if err := in.WriteTo(&b); err != nil {
			t.Fatal(err)
		}
		if b.Len() != in.BytesLen() {
			t.Fatalf("%v: wrote %d bytes, BytesLen %d", addr, b.Len(), in.BytesLen())
		}
		wire := b.Bytes()
		out, err := ReadPacket(bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("read %+v, wrote %+v", out, in)
		}
		for cut := 0; cut < len(wire); cut++ {
			if _, err := ReadPacket(bytes.NewReader(wire[:cut])); err == nil {
				t.Fatalf("%v: packet cut to %d bytes read", addr, cut)
			}
		}
	}
}
