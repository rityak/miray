package inbound_test

import (
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"
	"github.com/metacubex/mihomo/tunnel"

	vmess "github.com/metacubex/sing-vmess"
	M "github.com/metacubex/sing/common/metadata"
)

// T-118: what a VMess client wrote before Close reaches the destination through the
// real tunnel (early-data peek, dial, relay). The client is sing-vmess over a plain
// TCP connection, as sing-box dials it; the destination counts what arrives.
func TestInboundVMessUploadThenClose(t *testing.T) {
	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	got := make(chan int64, 1)
	go func() {
		for {
			c, err := sink.Accept()
			if err != nil {
				return
			}
			go func() {
				n, _ := io.Copy(io.Discard, c)
				c.Close()
				got <- n
			}()
		}
	}()
	tunnel.UpdateProxies(map[string]C.Proxy{"DIRECT": adapter.NewProxy(outbound.NewDirect())}, nil)
	tunnel.SetMode(tunnel.Direct)
	tunnel.OnRunning()

	in, err := inbound.NewVmess(&inbound.VmessOption{
		BaseOption: inbound.BaseOption{NameStr: "vmess_close_inbound", Listen: "127.0.0.1", Port: "0"},
		Users:      []inbound.VmessUser{{Username: "test", UUID: userUUID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Listen(tunnel.Tunnel); err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	client, err := vmess.NewClient(userUUID, "auto", 0)
	if err != nil {
		t.Fatal(err)
	}
	dst := M.SocksaddrFromNetIP(netip.MustParseAddrPort(sink.Addr().String()))
	for _, size := range []int64{32 << 10, 4 << 20} {
		raw, err := net.Dial("tcp", in.Address())
		if err != nil {
			t.Fatal(err)
		}
		conn := client.DialEarlyConn(raw, dst)
		if _, err := conn.Write(make([]byte, size)); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		select {
		case n := <-got:
			if n != size {
				t.Errorf("destination got %d of %d bytes written before Close", n, size)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("destination did not see the end of a %d-byte upload", size)
		}
	}
}
