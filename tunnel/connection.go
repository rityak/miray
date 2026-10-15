package tunnel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/common/handicap"
	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
)

// senderItem is a queued datagram: C.PacketAdapter (regular path) or
// C.UDPFastPacket (fast path, no Metadata).
type senderItem interface {
	Data() []byte
	Drop()
}

type packetSender struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan senderItem

	// destination NAT mapping
	originToTarget map[string]netip.Addr
	targetToOrigin map[netip.Addr]netip.Addr
	fastTarget     map[netip.Addr]netip.Addr       // originToTarget for IP destinations, keyed without formatting
	fastAddr       map[netip.AddrPort]*net.UDPAddr // resolved WriteTo address per original destination
	mappingMutex   sync.RWMutex

	// batched sends (S-005), owned by the Process goroutine
	batch      *batchWriter
	batchInit  bool
	batchPkts  []C.UDPFastPacket
	batchData  [][]byte
	batchAddrs []*net.UDPAddr
}

// maxBatch bounds one opportunistic drain of already queued datagrams.
const maxBatch = 64

// newPacketSender return a chan based C.PacketSender
// It ensures that packets can be sent sequentially and without blocking
func newPacketSender() C.PacketSender {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan senderItem, senderCapacity)
	return &packetSender{
		ctx:    ctx,
		cancel: cancel,
		ch:     ch,

		originToTarget: make(map[string]netip.Addr),
		targetToOrigin: make(map[netip.Addr]netip.Addr),
		fastTarget:     make(map[netip.Addr]netip.Addr),
		fastAddr:       make(map[netip.AddrPort]*net.UDPAddr),
	}
}

func (s *packetSender) AddMapping(originMetadata *C.Metadata, metadata *C.Metadata) {
	s.mappingMutex.Lock()
	defer s.mappingMutex.Unlock()
	originKey := originMetadata.String()
	originAddr := originMetadata.DstIP
	targetAddr := metadata.DstIP
	if addr := s.originToTarget[originKey]; !addr.IsValid() { // overwrite only if the record is illegal
		s.originToTarget[originKey] = targetAddr
	}
	if originMetadata.Host == "" && originAddr.IsValid() {
		if addr := s.fastTarget[originAddr]; !addr.IsValid() {
			s.fastTarget[originAddr] = targetAddr
		}
	}
	if addr := s.targetToOrigin[targetAddr]; !addr.IsValid() { // overwrite only if the record is illegal
		s.targetToOrigin[targetAddr] = originAddr
	}
}

func (s *packetSender) RestoreReadFrom(addr netip.Addr) netip.Addr {
	s.mappingMutex.RLock()
	defer s.mappingMutex.RUnlock()
	if originAddr := s.targetToOrigin[addr]; originAddr.IsValid() {
		return originAddr
	}
	return addr
}

func (s *packetSender) processPacket(pc C.PacketConn, packet C.PacketAdapter) {
	defer packet.Drop()
	s.forward(pc, packet.Metadata(), packet.Data())
}

// processFast writes a fast-path datagram; a destination not yet mapped in this
// session takes the regular resolve path once.
func (s *packetSender) processFast(pc C.PacketConn, packet C.UDPFastPacket) {
	defer packet.Drop()
	dst := packet.Destination()
	s.mappingMutex.RLock()
	addr := s.fastAddr[dst]
	s.mappingMutex.RUnlock()
	if addr == nil {
		addr = s.fastAddrFor(dst)
	}
	if addr != nil {
		_ = handleUDPToRemote(packet.Data(), pc, addr)
		return
	}
	s.forward(pc, &C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: dst.Addr(), DstPort: dst.Port()}, packet.Data())
}

// maxFastAddrs bounds the per-session WriteTo address cache.
const maxFastAddrs = 64

// fastAddrFor builds (and caches) the WriteTo address of a mapped destination,
// so steady traffic allocates no *net.UDPAddr per datagram. It returns nil for
// an unmapped destination.
func (s *packetSender) fastAddrFor(dst netip.AddrPort) *net.UDPAddr {
	s.mappingMutex.Lock()
	defer s.mappingMutex.Unlock()
	target := s.fastTarget[dst.Addr()]
	if !target.IsValid() {
		return nil
	}
	addr := net.UDPAddrFromAddrPort(netip.AddrPortFrom(target, dst.Port()))
	if len(s.fastAddr) < maxFastAddrs {
		s.fastAddr[dst] = addr // shared read-only by WriteTo calls of this session
	}
	return addr
}

func (s *packetSender) forward(pc C.PacketConn, metadata *C.Metadata, data []byte) {
	var addr *net.UDPAddr

	s.mappingMutex.RLock()
	targetAddr := s.originToTarget[metadata.String()]
	s.mappingMutex.RUnlock()

	if targetAddr.IsValid() {
		addr = net.UDPAddrFromAddrPort(netip.AddrPortFrom(targetAddr, metadata.DstPort))
	}

	if addr == nil {
		originMetadata := metadata  // save origin metadata
		metadata = metadata.Clone() // don't modify PacketAdapter's metadata

		_ = preHandleMetadata(metadata) // error was pre-checked
		metadata = metadata.Pure()
		if metadata.Host != "" {
			// TODO: ResolveUDP may take a long time to block the Process loop
			//       but we want keep sequence sending so can't open a new goroutine
			if err := pc.ResolveUDP(s.ctx, metadata); err != nil {
				log.Warnln("[UDP] Resolve Ip error: %s", err)
				return
			}
		}

		if !metadata.DstIP.IsValid() {
			log.Warnln("[UDP] Destination ip not valid: %#v", metadata)
			return
		}
		s.AddMapping(originMetadata, metadata)
		addr = metadata.UDPAddr()
	}
	_ = handleUDPToRemote(data, pc, addr)
}

func (s *packetSender) Process(pc C.PacketConn, proxy C.WriteBackProxy) {
	for {
		select {
		case <-s.ctx.Done():
			return // sender closed
		case item := <-s.ch:
			s.processItem(pc, proxy, item)
		}
	}
}

func (s *packetSender) processItem(pc C.PacketConn, proxy C.WriteBackProxy, item senderItem) {
	switch packet := item.(type) {
	case C.UDPFastPacket: // reply path keeps the writer of the flow's earlier packets
		s.processFastBatch(pc, proxy, packet)
	case C.PacketAdapter:
		if proxy != nil {
			proxy.UpdateWriteBack(packet)
		}
		s.processPacket(pc, packet)
	}
}

// processFastBatch takes the datagrams already queued behind first, without
// waiting for more, and sends them with one sendmmsg when the outbound is a raw
// UDP socket (S-005). A sparse datagram is sent alone and at once.
func (s *packetSender) processFastBatch(pc C.PacketConn, proxy C.WriteBackProxy, first C.UDPFastPacket) {
	if !s.batchInit {
		s.batchInit = true
		if ac, ok := pc.(*udpActivityConn); ok {
			s.batch = newBatchWriter(ac.PacketConn)
		}
	}
	if s.batch == nil {
		s.processFast(pc, first)
		return
	}
	pkts := append(s.batchPkts[:0], first)
	var pending senderItem
drain:
	for len(pkts) < maxBatch {
		select {
		case item := <-s.ch:
			if p, ok := item.(C.UDPFastPacket); ok {
				pkts = append(pkts, p)
				continue
			}
			pending = item
			break drain
		default:
			break drain
		}
	}
	if len(pkts) == 1 { // sendmmsg of one costs ~8% more than sendto (S-005)
		s.processFast(pc, first)
		pkts[0] = nil // the pooled packet is recycled after Drop
	} else {
		s.flushBatch(pc, pkts)
	}
	s.batchPkts = pkts[:0]
	if pending != nil {
		s.processItem(pc, proxy, pending)
	}
}

// flushBatch writes pkts in order; an unmapped destination splits the batch and
// takes the regular resolve path.
func (s *packetSender) flushBatch(pc C.PacketConn, pkts []C.UDPFastPacket) {
	datas, addrs := s.batchData[:0], s.batchAddrs[:0]
	flush := func() {
		if len(datas) == 0 {
			return
		}
		if s.batch.write(datas, addrs) == nil {
			pc.(*udpActivityConn).lastWrite.Store(time.Now().UnixNano())
		}
		datas, addrs = datas[:0], addrs[:0]
	}
	for _, p := range pkts {
		dst := p.Destination()
		s.mappingMutex.RLock()
		addr := s.fastAddr[dst]
		s.mappingMutex.RUnlock()
		if addr == nil {
			addr = s.fastAddrFor(dst)
		}
		if addr == nil {
			flush()
			s.forward(pc, &C.Metadata{NetWork: C.UDP, Type: C.TUN, DstIP: dst.Addr(), DstPort: dst.Port()}, p.Data())
			continue
		}
		if handicap.Packet(p.Data()) {
			continue
		}
		datas, addrs = append(datas, p.Data()), append(addrs, addr)
	}
	flush()
	for i, p := range pkts {
		p.Drop()
		pkts[i] = nil
	}
	for i := range datas[:cap(datas)] {
		datas[:cap(datas)][i] = nil // do not retain released buffers
	}
	s.batchData, s.batchAddrs = datas[:0], addrs[:0]
}

func (s *packetSender) dropAll() {
	for {
		select {
		case data := <-s.ch:
			data.Drop() // drop all data still in chan
		default:
			return // no data, exit goroutine
		}
	}
}

func (s *packetSender) Send(packet C.PacketAdapter) { s.send(packet) }

func (s *packetSender) send(packet senderItem) {
	select {
	case <-s.ctx.Done():
		packet.Drop() // sender closed before Send()
		return
	default:
	}

	// Two cases compile to a non-blocking send; a ctx.Done case here would cost
	// a full runtime.selectgo per packet (T-032). A send racing with Close is
	// caught by the check after it, and a closed sender is caught above.
	select {
	case s.ch <- packet:
		// Close can finish draining before a concurrent send enqueues its packet.
		select {
		case <-s.ctx.Done():
			s.dropAll()
		default:
		}
	default:
		packet.Drop() // chan is full
	}
}

func (s *packetSender) Close() {
	s.cancel()
	s.dropAll()
}

func (s *packetSender) DoSniff(metadata *C.Metadata) error { return nil }

func handleUDPToRemote(data []byte, pc C.PacketConn, addr *net.UDPAddr) error {
	if addr == nil {
		return errors.New("udp addr invalid")
	}
	if handicap.Packet(data) {
		return nil
	}

	_, err := pc.WriteTo(data, addr) // a successful write extends the idle timeout, see udpActivityConn
	return err
}

// udpActivityConn records the last successful outbound write. The reader in
// handleUDPToLocal extends its idle deadline lazily from it, instead of both
// directions resetting a runtime timer on every packet (S-001, F3).
type udpActivityConn struct {
	C.PacketConn
	lastWrite atomic.Int64 // unix nanoseconds
}

func (c *udpActivityConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(b, addr)
	if err == nil {
		c.lastWrite.Store(time.Now().UnixNano())
	}
	return n, err
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func handleUDPToLocal(writeBack C.WriteBack, pc *udpActivityConn, sender C.PacketSender, key string, oAddrPort netip.AddrPort) {
	defer func() {
		sender.Close()
		_ = pc.Close()
		closeAllLocalCoon(key)
		natTable.Delete(key)
	}()

	lastRead := time.Now().UnixNano()
	_ = pc.SetReadDeadline(time.Unix(0, lastRead).Add(udpTimeout))
	for {
		data, put, from, err := pc.WaitReadFrom()
		if err != nil {
			if put != nil {
				put() // a partial read can carry a buffer together with the error
			}
			if isTimeout(err) {
				// the session is idle only if neither direction was active for udpTimeout
				last := lastRead
				if w := pc.lastWrite.Load(); w > last {
					last = w
				}
				if next := time.Unix(0, last).Add(udpTimeout); next.After(time.Now()) {
					_ = pc.SetReadDeadline(next)
					continue
				}
			}
			return
		}
		lastRead = time.Now().UnixNano()
		if handicap.Packet(data) {
			if put != nil {
				put()
			}
			continue
		}

		fromUDPAddr, isUDPAddr := from.(*net.UDPAddr)
		if !isUDPAddr {
			fromUDPAddr = net.UDPAddrFromAddrPort(oAddrPort) // oAddrPort was Unmapped
			log.Warnln("server return a [%T](%s) which isn't a *net.UDPAddr, force replace to (%s), this may be caused by a wrongly implemented server", from, from, oAddrPort)
		} else if fromUDPAddr == nil {
			fromUDPAddr = net.UDPAddrFromAddrPort(oAddrPort) // oAddrPort was Unmapped
			log.Warnln("server return a nil *net.UDPAddr, force replace to (%s), this may be caused by a wrongly implemented server", oAddrPort)
		}

		fromAddrPort := fromUDPAddr.AddrPort()
		fromAddr := fromAddrPort.Addr().Unmap()

		// restore DestinationNAT
		fromAddr = sender.RestoreReadFrom(fromAddr).Unmap()

		fromAddrPort = netip.AddrPortFrom(fromAddr, fromAddrPort.Port())

		_, err = writeBack.WriteBack(data, net.UDPAddrFromAddrPort(fromAddrPort))
		if put != nil {
			put()
		}
		if err != nil {
			return
		}
	}
}

func closeAllLocalCoon(lAddr string) {
	natTable.RangeForLocalConn(lAddr, func(key string, value *net.UDPConn) bool {
		conn := value

		conn.Close()
		log.Debugln("Closing TProxy local conn... lAddr=%s rAddr=%s", lAddr, key)
		return true
	})
}

func handleSocket(inbound, outbound net.Conn) {
	N.Relay(inbound, outbound)
}
