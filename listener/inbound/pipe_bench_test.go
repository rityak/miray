package inbound_test

// T-077: protocol path in one process — outbound client → loopback → inbound
// server → sink, no TUN, no stand. Answers "is the protocol path cheaper" in
// seconds; the stand is for what the kernel and the link add (pacing, queues).
//
//	go test ./listener/inbound -run '^$' -bench Pipe -cpu 1 -count 10 > new.txt
//	benchstat old.txt new.txt
//
// cpu-ns/op is user+sys of the whole process (client and server together).

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/inbound"
	"github.com/metacubex/mihomo/log"
)

const pipeChunk = 32 << 10

type pipeServer interface {
	Listen(C.Tunnel) error
	Close() error
	Address() string
}

type pipeCase struct {
	name string
	udp  bool
	in   func(port string) (pipeServer, error)
	out  func(addr netip.AddrPort, tunnel *TestTunnel) (C.ProxyAdapter, error)
}

func freePort(b *testing.B) string {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0") // free for udp; tcp is checked by Listen
	if err != nil {
		b.Fatal(err)
	}
	defer probe.Close()
	return strconv.Itoa(probe.LocalAddr().(*net.UDPAddr).Port)
}

func base(name, port string) inbound.BaseOption {
	return inbound.BaseOption{NameStr: name, Listen: "127.0.0.1", Port: port}
}

var pipeCases = []pipeCase{
	{
		name: "ss2022", udp: true,
		in: func(port string) (pipeServer, error) {
			return inbound.NewShadowSocks(&inbound.ShadowSocksOption{BaseOption: base("ss", port),
				Password: shadowsocksPassword16, Cipher: "2022-blake3-aes-128-gcm", UDP: true})
		},
		out: func(a netip.AddrPort, _ *TestTunnel) (C.ProxyAdapter, error) {
			return outbound.NewShadowSocks(outbound.ShadowSocksOption{Name: "ss", Server: a.Addr().String(), Port: int(a.Port()),
				Password: shadowsocksPassword16, Cipher: "2022-blake3-aes-128-gcm", UDP: true})
		},
	},
	{
		name: "trojan",
		in: func(port string) (pipeServer, error) {
			return inbound.NewTrojan(&inbound.TrojanOption{BaseOption: base("trojan", port),
				Users: []inbound.TrojanUser{{Username: "u", Password: userUUID}}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey})
		},
		out: func(a netip.AddrPort, t *TestTunnel) (C.ProxyAdapter, error) {
			o := outbound.TrojanOption{Name: "trojan", Server: a.Addr().String(), Port: int(a.Port()), Password: userUUID, Fingerprint: tlsFingerprint}
			o.DialerForAPI, o.TunnelForAPI = t.NewDialer(), t
			return outbound.NewTrojan(o)
		},
	},
	{
		name: "vless-vision",
		in: func(port string) (pipeServer, error) {
			return inbound.NewVless(&inbound.VlessOption{BaseOption: base("vless", port),
				Users: []inbound.VlessUser{{Username: "u", UUID: userUUID, Flow: "xtls-rprx-vision"}}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey})
		},
		out: func(a netip.AddrPort, t *TestTunnel) (C.ProxyAdapter, error) {
			o := outbound.VlessOption{Name: "vless", Server: a.Addr().String(), Port: int(a.Port()), UUID: userUUID,
				TLS: true, Fingerprint: tlsFingerprint, Flow: "xtls-rprx-vision"}
			o.DialerForAPI, o.TunnelForAPI = t.NewDialer(), t
			return outbound.NewVless(o)
		},
	},
	{
		name: "hy2", udp: true,
		in: func(port string) (pipeServer, error) {
			return inbound.NewHysteria2(&inbound.Hysteria2Option{BaseOption: base("hy2", port),
				Users: map[string]string{"u": userUUID}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey})
		},
		out: func(a netip.AddrPort, t *TestTunnel) (C.ProxyAdapter, error) {
			o := outbound.Hysteria2Option{Name: "hy2", Server: a.Addr().String(), Port: int(a.Port()), Password: userUUID, Fingerprint: tlsFingerprint}
			o.DialerForAPI, o.TunnelForAPI = t.NewDialer(), t
			return outbound.NewHysteria2(o)
		},
	},
	{
		name: "tuic", udp: true,
		in: func(port string) (pipeServer, error) {
			return inbound.NewTuic(&inbound.TuicOption{BaseOption: base("tuic", port),
				Users: map[string]string{userUUID: userUUID}, Certificate: tlsCertificate, PrivateKey: tlsPrivateKey})
		},
		out: func(a netip.AddrPort, t *TestTunnel) (C.ProxyAdapter, error) {
			o := outbound.TuicOption{Name: "tuic", Server: a.Addr().String(), Port: int(a.Port()),
				UUID: userUUID, Password: userUUID, Fingerprint: tlsFingerprint, UdpRelayMode: "native"}
			o.DialerForAPI, o.TunnelForAPI = t.NewDialer(), t
			return outbound.NewTuic(o)
		},
	},
}

// startPipe brings up the pair; the caller sets the tunnel handlers first.
func startPipe(b *testing.B, c pipeCase, tunnel *TestTunnel) C.ProxyAdapter {
	in, err := c.in(freePort(b))
	if err != nil {
		b.Fatal(err)
	}
	if err := in.Listen(tunnel); err != nil {
		b.Skip("listen:", err) // the port was taken for tcp in between
	}
	b.Cleanup(func() { in.Close() })
	addr := strings.Split(in.Address(), ",")[0] // ss lists tcp,udp
	out, err := c.out(netip.MustParseAddrPort(addr), tunnel)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { out.Close() })
	return out
}

var pipeDst = netip.MustParseAddrPort("192.0.2.1:443")

func pipeMetadata(network C.NetWork) *C.Metadata {
	return &C.Metadata{NetWork: network, DstIP: pipeDst.Addr(), DstPort: pipeDst.Port()}
}

// TCP up: the client writes b.N chunks, the server side discards them.
// TCP down: the client asks for b.N chunks, the server side writes them.
func benchPipeTCP(b *testing.B, c pipeCase, down bool) {
	tunnel := NewHttpTestTunnel()
	b.Cleanup(func() { tunnel.Close() })
	done := make(chan int64, 1)
	tunnel.HandleTCPConnFn = func(conn net.Conn, _ *C.Metadata) {
		defer conn.Close()
		if !down {
			n, _ := io.Copy(io.Discard, conn)
			done <- n
			return
		}
		var hdr [8]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		chunk := make([]byte, pipeChunk)
		for left := binary.BigEndian.Uint64(hdr[:]); left > 0; left-- {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	}
	out := startPipe(b, c, tunnel)
	conn, err := out.DialContext(context.Background(), pipeMetadata(C.TCP))
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	chunk := make([]byte, pipeChunk)
	b.SetBytes(pipeChunk)
	b.ReportAllocs()
	cpu := startCPU()
	b.ResetTimer()
	if down {
		var hdr [8]byte
		binary.BigEndian.PutUint64(hdr[:], uint64(b.N))
		if _, err := conn.Write(hdr[:]); err != nil {
			b.Fatal(err)
		}
		if n, err := io.CopyN(io.Discard, conn, int64(b.N)*pipeChunk); err != nil {
			b.Fatalf("read %d: %v", n, err)
		}
	} else {
		for i := 0; i < b.N; i++ {
			if _, err := conn.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		conn.Close()
		select {
		case n := <-done:
			if n != int64(b.N)*pipeChunk {
				b.Fatalf("server got %d of %d bytes", n, int64(b.N)*pipeChunk)
			}
		case <-time.After(30 * time.Second):
			b.Fatal("server did not finish")
		}
	}
	b.StopTimer()
	cpu.report(b)
}

// UDP echo of 1200-byte datagrams with at most pipeWindow in flight: cost of
// a datagram up and its reply down. Loss (QUIC on a busy host) is reported,
// not fatal; a lost datagram costs a 20 ms wait, so loss-% must stay ~0 for
// ns/op to mean anything.
const pipeWindow = 32

func benchPipeUDP(b *testing.B, c pipeCase) {
	tunnel := NewHttpTestTunnel()
	b.Cleanup(func() { tunnel.Close() })
	tunnel.HandleUDPPacketFn = func(packet C.UDPPacket, metadata *C.Metadata) {
		_, _ = packet.WriteBack(packet.Data(), metadata.UDPAddr())
		packet.Drop()
	}
	out := startPipe(b, c, tunnel)
	pc, err := out.ListenPacketContext(context.Background(), pipeMetadata(C.UDP))
	if err != nil {
		b.Fatal(err)
	}
	defer pc.Close()

	var got atomic.Int64
	recv := make(chan struct{}, pipeWindow)
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
			got.Add(1)
			select {
			case recv <- struct{}{}:
			default:
			}
		}
	}()

	dst := net.UDPAddrFromAddrPort(pipeDst)
	payload := make([]byte, 1200)
	// warm the session up: the first datagrams of a QUIC session wait for auth (T-076)
	for i := 0; got.Load() == 0 && i < 100; i++ {
		_, _ = pc.WriteTo(payload, dst)
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() == 0 {
		b.Fatal("no echo")
	}
	time.Sleep(50 * time.Millisecond)
	start := got.Load()

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	cpu := startCPU()
	b.ResetTimer()
	var lost int64
	wait := time.NewTimer(time.Hour) // one timer: time.After would add its allocations to the path's
	for i := 0; i < b.N; i++ {
		for int64(i)-lost-(got.Load()-start) >= pipeWindow {
			if !wait.Stop() { // go 1.20 timers: drain a fire nobody read
				select {
				case <-wait.C:
				default:
				}
			}
			wait.Reset(20 * time.Millisecond)
			select {
			case <-recv:
			case <-wait.C:
				lost = int64(i) - (got.Load() - start) // whatever is still out is gone
			}
		}
		if _, err := pc.WriteTo(payload, dst); err != nil {
			b.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for got.Load()-start < int64(b.N) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	b.StopTimer()
	cpu.report(b)
	b.ReportMetric(100*float64(int64(b.N)-(got.Load()-start))/float64(b.N), "loss-%")
}

func BenchmarkPipe(b *testing.B) {
	log.SetLevel(log.SILENT) // listener logs would split the benchmark lines
	for _, c := range pipeCases {
		c := c
		b.Run(c.name+"/tcp-up", func(b *testing.B) { benchPipeTCP(b, c, false) })
		b.Run(c.name+"/tcp-down", func(b *testing.B) { benchPipeTCP(b, c, true) })
		if c.udp {
			b.Run(c.name+"/udp-echo", func(b *testing.B) { benchPipeUDP(b, c) })
		}
	}
}
