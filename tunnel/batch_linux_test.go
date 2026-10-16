//go:build linux

package tunnel

import (
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/packet"
	"github.com/metacubex/mihomo/component/nat"
	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

// sendmmsg delivers every datagram in order, with its exact payload, and the
// bytes are accounted once (S-005).
func TestBatchWriterDelivers(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	conn, err := net.ListenUDP("udp", nil) // dual-stack, like DIRECT
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var counted int64
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	dst := sink.LocalAddr().(*net.UDPAddr)
	var datas [][]byte
	var addrs []*net.UDPAddr
	for i := 0; i < maxBatch; i++ {
		datas = append(datas, []byte{byte(i), byte(i), byte(i)})
		addrs = append(addrs, dst)
	}
	if err := w.write(datas, addrs); err != nil {
		t.Fatal(err)
	}
	if counted != int64(3*maxBatch) {
		t.Fatalf("counted %d bytes, want %d", counted, 3*maxBatch)
	}
	buf := make([]byte, 16)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < maxBatch; i++ {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != 3 || buf[0] != byte(i) {
			t.Fatalf("datagram %d: %v %v", i, buf[:n], err)
		}
	}
}

// A chain with an unknown layer (any protocol wrapper) must not get a batch
// writer: writing to the socket below it would bypass that layer.
func TestBatchWriterRefusesUnknownLayer(t *testing.T) {
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	var pc C.PacketConn = &lifecyclePC{epc: packet.NewEnhancePacketConn(udp)}
	if w := newBatchWriter(pc); w != nil {
		t.Fatal("batch writer created through an unknown layer")
	}
}

// A same-destination run goes out as one UDP_SEGMENT send and still arrives as
// separate datagrams, in order, including a shorter last one (S-007).
func TestBatchWriterGSO(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var counted int64
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	dst, dst2 := sink.LocalAddr().(*net.UDPAddr), other.LocalAddr().(*net.UDPAddr)
	sizes := []int{1200, 1200, 1200, 800, 1200, 1200, 500}
	var datas [][]byte
	var addrs []*net.UDPAddr
	var want int64
	for i, size := range sizes {
		d := make([]byte, size)
		d[0] = byte(i)
		datas = append(datas, d)
		addrs = append(addrs, dst)
		want += int64(size)
	}
	datas = append(datas, []byte{99}) // another destination ends the run
	addrs = append(addrs, dst2)
	want++
	if got := w.gsoRun(datas, addrs); got != 4 {
		t.Fatalf("first run = %d, want 4 (equal sizes, shorter last)", got)
	}
	if err := w.write(datas, addrs); err != nil {
		t.Fatal(err)
	}
	if w.noGSO {
		t.Skip("kernel refused UDP_SEGMENT")
	}
	if counted != want {
		t.Fatalf("counted %d bytes, want %d", counted, want)
	}
	buf := make([]byte, 2048)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i, size := range sizes {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != size || buf[0] != byte(i) {
			t.Fatalf("datagram %d: len %d first %d err %v, want len %d", i, n, buf[0], err, size)
		}
	}
	_ = other.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _, err := other.ReadFrom(buf); err != nil || n != 1 || buf[0] != 99 {
		t.Fatalf("other destination: %v %v", buf[:n], err)
	}
}

// A datagram the kernel refuses (here port 0: EINVAL) costs only itself, as a
// sendto per datagram would: the ones after it in the batch still go out. A
// failed sendmmsg reports n = -1 through x/net, which must not reach a slice.
func TestBatchWriterSkipsFailedDatagram(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var counted int64
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(n int64) { counted += n }}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	dst := sink.LocalAddr().(*net.UDPAddr)
	bad := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	datas := [][]byte{{0}, {1, 1}, {2}, {3, 3, 3}}
	addrs := []*net.UDPAddr{bad, dst, bad, dst}
	if err := w.write(datas, addrs); err == nil {
		t.Fatal("refused datagrams reported no error")
	}
	if counted != 5 {
		t.Fatalf("counted %d bytes, want 5 (the two delivered datagrams)", counted)
	}
	buf := make([]byte, 16)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for _, want := range []int{2, 3} {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != want {
			t.Fatalf("got %v %v, want a %d-byte datagram", buf[:n], err, want)
		}
	}
}

// A batch with a refused datagram still marks the session active: the idle
// timeout must not close a flow whose other datagrams go out.
func TestBatchWriterActivityWithRefusedDatagram(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	w := &batchWriter{v6: ipv6.NewPacketConn(conn), udp: conn, msgs: make([]ipv6.Message, maxBatch), count: func(int64) {}}
	for i := range w.msgs {
		w.msgs[i].Buffers = make([][]byte, 1)
	}
	ac := &udpActivityConn{}
	trackActivity(w, ac)
	bad := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	if err := w.write([][]byte{{1}, {2, 2}}, []*net.UDPAddr{bad, sink.LocalAddr().(*net.UDPAddr)}); err == nil {
		t.Fatal("refused datagram reported no error")
	}
	if ac.lastWrite.Load() == 0 {
		t.Fatal("a batch that sent a datagram did not extend the idle timeout")
	}
}

func TestIsGSOError(t *testing.T) {
	for _, err := range []error{unix.EIO, unix.EINVAL, unix.ENOPROTOOPT, unix.EOPNOTSUPP,
		&net.OpError{Op: "write", Err: os.NewSyscallError("sendmsg", unix.EIO)}} {
		if !isGSOError(err) {
			t.Errorf("%v: want GSO refusal", err)
		}
	}
	for _, err := range []error{nil, unix.ENOBUFS, unix.ECONNREFUSED, net.ErrClosed} {
		if isGSOError(err) {
			t.Errorf("%v: transient error disables GSO", err)
		}
	}
}

// testCorker is a connected socket that collects while corked, like the ss
// session socket (outbound.connectedUDPConn).
type testCorker struct {
	C.PacketConn
	udp     *net.UDPConn
	corked  bool
	queue   [][]byte
	batches []int
}

func (c *testCorker) WriteTo(b []byte, _ net.Addr) (int, error) {
	if c.corked {
		c.queue = append(c.queue, append([]byte(nil), b...))
		return len(b), nil
	}
	return c.udp.Write(b)
}
func (c *testCorker) Cork() { c.corked, c.queue = true, c.queue[:0] }
func (c *testCorker) Uncork() [][]byte {
	c.corked = false
	c.batches = append(c.batches, len(c.queue))
	return c.queue
}
func (c *testCorker) RawUDP() *net.UDPConn { return c.udp }

// testCipher stands for an encrypting outbound above the socket.
type testCipher struct {
	C.PacketConn
	inner *testCorker
}

func (c *testCipher) WriteTo(b []byte, addr net.Addr) (int, error) {
	return c.inner.WriteTo(append([]byte{0xEE}, b...), addr)
}
func (c *testCipher) Upstream() any { return c.inner }

// Datagrams queued on a session whose outbound encrypts above a connected
// socket go out in order, the queued ones as one batch (T-059).
func TestCorkedBatchUnderCipher(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	// connected the way outbound.connectUDP does it: a dual-stack socket from
	// ListenUDP, connected with a raw connect, which Go does not know about
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	sa := &unix.SockaddrInet6{Port: sink.LocalAddr().(*net.UDPAddr).Port}
	copy(sa.Addr[:], net.IPv4(127, 0, 0, 1).To16())
	rc, _ := udp.SyscallConn()
	var cerr error
	if err := rc.Control(func(fd uintptr) { cerr = unix.Connect(int(fd), sa) }); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	corker := &testCorker{udp: udp}
	pc := &udpActivityConn{PacketConn: &testCipher{inner: corker}}

	s := newPacketSender().(*packetSender)
	defer s.Close()
	src := &net.UDPAddr{IP: net.IPv4(198, 18, 0, 59), Port: 40059}
	md := &C.Metadata{NetWork: C.UDP, Type: C.SOCKS5, DstIP: netip.MustParseAddr("192.0.2.59"), DstPort: 443}
	s.AddMapping(md, md)
	const count = 20
	for i := 0; i < count; i++ { // queued before the sender runs
		m := *md
		payload := append([]byte{byte(i)}, make([]byte, i%3)...) // sizes 2,3,4,...: GSO runs and sendmmsg
		s.Send(C.NewPacketAdapter(&benchPacket{payload, src}, &m))
	}
	go s.Process(pc, nil)

	buf := make([]byte, 16)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < count; i++ {
		n, _, err := sink.ReadFrom(buf)
		if err != nil || n != 2+i%3 || buf[0] != 0xEE || buf[1] != byte(i) {
			t.Fatalf("datagram %d: %v %v", i, buf[:n], err)
		}
	}
	if len(corker.batches) == 0 || corker.batches[0] < 2 {
		t.Fatalf("batches %v, want the queued datagrams together", corker.batches)
	}
}

type replyPacket struct {
	benchPacket
	reply any
}

func (p *replyPacket) ReplyConn() any { return p.reply }

type fakeHolder struct{ flushInit bool }

func (*fakeHolder) Hold()          {}
func (*fakeHolder) Release() error { return nil }
func (h *fakeHolder) InitFlush(func(*net.UDPConn) func([][]byte, []*net.UDPAddr) error) {
	h.flushInit = true
}

type bufferedPC struct {
	C.PacketConn
	n int
}

func (b *bufferedPC) Buffered() int { return b.n }

// The listener socket of a reply is found through the write-back proxy and the
// packet adapter tunnel wraps every packet in (T-068: the adapter hid it once).
func TestReplyBatchFound(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	h := &fakeHolder{}
	wb := nat.NewWriteBackProxy(C.NewPacketAdapter(&replyPacket{benchPacket{addr: addr}, h}, &C.Metadata{}))
	pc := &udpActivityConn{PacketConn: &bufferedPC{n: 3}}
	got, buffered := replyBatch(wb, pc)
	if got != h || buffered == nil || buffered() != 3 || !h.flushInit {
		t.Fatalf("replyBatch = %v, flush set %v", got, h.flushInit)
	}
	plain := nat.NewWriteBackProxy(C.NewPacketAdapter(&benchPacket{addr: addr}, &C.Metadata{}))
	if got, _ := replyBatch(plain, pc); got != nil {
		t.Fatalf("a listener without batching got %v", got)
	}
}
