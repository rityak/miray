package sing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter/inbound"
	"github.com/metacubex/mihomo/common/diag"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	"github.com/gofrs/uuid/v5"
	mux "github.com/metacubex/sing-mux"
	vmess "github.com/metacubex/sing-vmess"
	"github.com/metacubex/sing-vmess/packetaddr"
	"github.com/metacubex/sing/common"
	"github.com/metacubex/sing/common/buf"
	"github.com/metacubex/sing/common/bufio"
	"github.com/metacubex/sing/common/bufio/deadline"
	E "github.com/metacubex/sing/common/exceptions"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
	"github.com/metacubex/sing/common/uot"
)

const UDPTimeout = 5 * time.Minute
const ICMPTimeout = 10 * time.Second

type ListenerConfig struct {
	Tunnel     C.Tunnel
	Type       C.Type
	Additions  []inbound.Addition
	UDPTimeout time.Duration
	MuxOption  MuxOption
}

type MuxOption struct {
	Padding bool          `yaml:"padding" json:"padding,omitempty"`
	Brutal  BrutalOptions `yaml:"brutal" json:"brutal,omitempty"`
}

type BrutalOptions struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Up      string `yaml:"up" json:"up,omitempty"`
	Down    string `yaml:"down" json:"down,omitempty"`
}

type ListenerHandler struct {
	ListenerConfig
	handlerId  uuid.UUID
	muxService *mux.Service
}

func UpstreamMetadata(metadata M.Metadata) M.Metadata {
	return M.Metadata{
		Source:      metadata.Source,
		Destination: metadata.Destination,
	}
}

func ConvertMetadata(metadata *C.Metadata) M.Metadata {
	return M.Metadata{
		Protocol:    metadata.Type.String(),
		Source:      M.SocksaddrFrom(metadata.SrcIP, metadata.SrcPort),
		Destination: M.ParseSocksaddrHostPort(metadata.String(), metadata.DstPort),
	}
}

func NewListenerHandler(lc ListenerConfig) (h *ListenerHandler, err error) {
	h = &ListenerHandler{ListenerConfig: lc}
	h.handlerId = utils.NewUUIDV4()
	h.muxService, err = mux.NewService(mux.ServiceOptions{
		NewStreamContext: func(ctx context.Context, conn net.Conn) context.Context {
			return ctx
		},
		Logger:  log.SingInfoToDebugLogger, // convert sing-mux info log to debug
		Handler: h,
		Padding: lc.MuxOption.Padding,
		Brutal: mux.BrutalOptions{
			Enabled:    lc.MuxOption.Brutal.Enabled,
			SendBPS:    utils.StringToBps(lc.MuxOption.Brutal.Up),
			ReceiveBPS: utils.StringToBps(lc.MuxOption.Brutal.Down),
		},
	})
	return
}

func (h *ListenerHandler) IsSpecialFqdn(fqdn string) bool {
	switch fqdn {
	case mux.Destination.Fqdn,
		vmess.MuxDestination.Fqdn,
		uot.MagicAddress,
		uot.LegacyMagicAddress:
		return true
	default:
		return false
	}
}

func (h *ListenerHandler) ParseSpecialFqdn(ctx context.Context, conn net.Conn, metadata M.Metadata) error {
	switch metadata.Destination.Fqdn {
	case mux.Destination.Fqdn:
		return h.muxService.NewConnection(ctx, conn, UpstreamMetadata(metadata))
	case vmess.MuxDestination.Fqdn:
		return vmess.HandleMuxConnection(ctx, conn, metadata, h)
	case uot.MagicAddress:
		request, err := uot.ReadRequest(conn)
		if err != nil {
			return E.Cause(err, "read UoT request")
		}
		metadata.Destination = request.Destination
		return h.NewPacketConnection(ctx, uot.NewConn(conn, *request), metadata)
	case uot.LegacyMagicAddress:
		metadata.Destination = M.Socksaddr{Addr: netip.IPv4Unspecified()}
		return h.NewPacketConnection(ctx, uot.NewConn(conn, uot.Request{}), metadata)
	}
	return errors.New("not special fqdn")
}

func (h *ListenerHandler) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {
	if h.IsSpecialFqdn(metadata.Destination.Fqdn) {
		return h.ParseSpecialFqdn(ctx, conn, metadata)
	}

	if deadline.NeedAdditionalReadDeadline(conn) {
		conn = N.NewDeadlineConn(conn) // conn from sing should check NeedAdditionalReadDeadline
	}

	cMetadata := &C.Metadata{
		NetWork: C.TCP,
		Type:    h.Type,
	}
	if metadata.Source.IsIP() && metadata.Source.Fqdn == "" {
		cMetadata.RawSrcAddr = metadata.Source.Unwrap().TCPAddr()
	}
	if metadata.Destination.IsIP() && metadata.Destination.Fqdn == "" {
		cMetadata.RawDstAddr = metadata.Destination.Unwrap().TCPAddr()
	}
	inbound.ApplyAdditions(cMetadata, inbound.WithDstAddr(metadata.Destination), inbound.WithSrcAddr(metadata.Source), inbound.WithInAddr(conn.LocalAddr()))
	inbound.ApplyAdditions(cMetadata, h.Additions...)
	inbound.ApplyAdditions(cMetadata, getAdditions(ctx)...)

	h.Tunnel.HandleTCPConn(conn, cMetadata) // this goroutine must exit after conn unused
	return nil
}

func (h *ListenerHandler) NewPacketConnection(ctx context.Context, conn network.PacketConn, metadata M.Metadata) error {
	if metadata.Destination.Fqdn == packetaddr.SeqPacketMagicAddress {
		conn = packetaddr.NewConn(bufio.NewNetPacketConn(conn), M.Socksaddr{})
	}

	connID := utils.NewUUIDV4().String() // make a new SNAT key
	reply := getReplyConn(ctx)

	defer func() { _ = conn.Close() }()
	mutex := sync.Mutex{}
	writer := bufio.NewNetPacketWriter(conn) // a new interface to set nil in defer
	defer func() {
		mutex.Lock() // this goroutine must exit after all conn.WritePacket() is not running
		defer mutex.Unlock()
		writer = nil
	}()
	// Per connection, not per datagram (T-078): the source, its SNAT key and the
	// additions do not change while the connection lives.
	rAddr := N.NewCustomAddr(h.Type.String(), connID, metadata.Source.UDPAddr()) // for tunnel's handleUDPConn
	lAddr := conn.LocalAddr()
	if inAddr := getInAddr(ctx); inAddr != nil {
		lAddr = inAddr
	}
	rawSrc := rawUDPAddr(metadata.Source)
	// The metadata less the destination is the same for every datagram (T-128): built
	// once with the additions in their order, copied per datagram, which then only
	// takes its destination (no addition sets one).
	base := C.Metadata{NetWork: C.UDP, Type: h.Type, RawSrcAddr: rawSrc}
	inbound.ApplyAdditions(&base, inbound.WithSrcAddr(metadata.Source), inbound.WithInAddr(lAddr))
	inbound.ApplyAdditions(&base, h.Additions...)
	inbound.ApplyAdditions(&base, getAdditions(ctx)...)
	var (
		lastDst    M.Socksaddr
		lastRawDst net.Addr // of lastDst: a flow keeps its destination
		haveLast   bool
	)
	// Once the session is established, a datagram to an IP goes straight to it,
	// without Metadata or the worker queue, as on a TUN flow (T-133): the session
	// was routed once, by its first datagram.
	var flow C.UDPFlow
	if ft, ok := h.Tunnel.(C.UDPFlowTunnel); ok {
		flow = ft.NewUDPFlow()
	}
	rwOptions := network.ReadWaitOptions{}
	readWaiter, isReadWaiter := bufio.CreatePacketReadWaiter(conn)
	if isReadWaiter {
		readWaiter.InitializeReadWaiter(rwOptions)
	}
	for {
		var (
			buff *buf.Buffer
			dest M.Socksaddr
			err  error
		)
		if isReadWaiter {
			buff, dest, err = readWaiter.WaitReadPacket()
		} else {
			buff = rwOptions.NewPacketBuffer()
			dest, err = conn.ReadPacket(buff)
			if buff != nil {
				rwOptions.PostReturn(buff)
			}
		}
		if err != nil {
			buff.Release()
			if ShouldIgnorePacketError(err) {
				break
			}
			return err
		}
		if flow != nil && dest.IsIP() && dest.Port != 0 {
			dst := dest.AddrPort()
			fp := fastPacketPool.Get().(*fastPacket)
			fp.buff, fp.dst = buff, netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
			if flow.Send(fp) {
				continue
			}
			fp.buff = nil // not taken: the buffer continues on the regular path
			fastPacketPool.Put(fp)
		}
		cPacket := &packet{
			writer: &writer,
			mutex:  &mutex,
			rAddr:  rAddr,
			lAddr:  lAddr,
			buff:   buff,
			reply:  reply,
			flow:   flow,
		}
		if !haveLast || dest != lastDst {
			lastDst, lastRawDst, haveLast = dest, rawUDPAddr(dest), true
		}
		cMetadata := new(C.Metadata)
		*cMetadata = base
		cMetadata.RawDstAddr = lastRawDst
		setDestination(cMetadata, dest)
		h.Tunnel.HandleUDPPacket(cPacket, cMetadata)
	}
	return nil
}

type localAddr interface {
	LocalAddr() net.Addr
}

func (h *ListenerHandler) NewPacket(ctx context.Context, key netip.AddrPort, buffer *buf.Buffer, metadata M.Metadata, init func(natConn network.PacketConn) network.PacketWriter) {
	// Fast path (S-004): a datagram of an established TUN flow goes straight to
	// its session, without Metadata, a writer, or the worker queue.
	if h.Type == C.TUN && metadata.Destination.IsIP() {
		if fast, ok := h.Tunnel.(C.UDPFastTunnel); ok {
			dst := metadata.Destination.AddrPort()
			packet := fastPacketPool.Get().(*fastPacket)
			packet.buff, packet.dst = buffer, netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
			if fast.HandleUDPPacketFast(netip.AddrPortFrom(key.Addr().Unmap(), key.Port()), packet) {
				return
			}
			packet.buff = nil // not taken: the buffer continues on the regular path
			fastPacketPool.Put(packet)
		}
	}
	writer := bufio.NewNetPacketWriter(init(nil))
	mutex := sync.Mutex{}
	cPacket := &packet{
		writer: &writer,
		mutex:  &mutex,
		rAddr:  metadata.Source.UDPAddr(),
		buff:   buffer,
		reply:  getReplyConn(ctx),
	}
	if h.Type != C.TUN { // make the handler-related SNAT key for not TUN listener
		connID := fmt.Sprintf("%s:%s", h.handlerId, key)
		cPacket.rAddr = N.NewCustomAddr(h.Type.String(), connID, cPacket.rAddr) // for tunnel's handleUDPConn
	}
	if conn, ok := common.Cast[localAddr](writer); ok { // tun does not have real inAddr
		cPacket.lAddr = conn.LocalAddr()
	}
	h.handlePacket(cPacket, metadata.Source, metadata.Destination, rawUDPAddr(metadata.Source), getAdditions(ctx))
}

// setDestination is inbound.WithDstAddr(dst) without boxing dst into a net.Addr:
// the AddrPort branch of Metadata.SetRemoteAddr, the only one an M.Socksaddr takes
// with a port.
func setDestination(m *C.Metadata, dst M.Socksaddr) {
	if dst.Port == 0 {
		_ = m.SetRemoteAddr(dst)
		return
	}
	m.DstPort = dst.Port
	if dst.Addr.IsValid() {
		m.DstIP = dst.Addr.Unmap()
	} else {
		m.Host = dst.Fqdn
	}
}

// rawUDPAddr is the RawSrcAddr/RawDstAddr of a datagram: nil for a domain.
func rawUDPAddr(addr M.Socksaddr) net.Addr {
	if addr.IsIP() && addr.Fqdn == "" {
		return addr.Unwrap().UDPAddr()
	}
	return nil
}

func (h *ListenerHandler) handlePacket(cPacket *packet, source M.Socksaddr, destination M.Socksaddr, rawSrc net.Addr, additions []inbound.Addition) {
	cMetadata := &C.Metadata{
		NetWork:    C.UDP,
		Type:       h.Type,
		RawSrcAddr: rawSrc,
		RawDstAddr: rawUDPAddr(destination),
	}
	inbound.ApplyAdditions(cMetadata, inbound.WithDstAddr(destination), inbound.WithSrcAddr(source), inbound.WithInAddr(cPacket.InAddr()))
	inbound.ApplyAdditions(cMetadata, h.Additions...)
	inbound.ApplyAdditions(cMetadata, additions...)

	h.Tunnel.HandleUDPPacket(cPacket, cMetadata)
}

func (h *ListenerHandler) NewError(ctx context.Context, err error) {
	log.Warnln("%s listener get error: %+v", h.Type.String(), err)
	if diag.Enabled && h.Type == C.TUN {
		diag.TunError(err)
	}
}

func (h *ListenerHandler) TypeMutation(typ C.Type) *ListenerHandler {
	handler := *h
	handler.Type = typ
	return &handler
}

func ShouldIgnorePacketError(err error) bool {
	// ignore simple error
	if E.IsTimeout(err) || E.IsClosed(err) || E.IsCanceled(err) {
		return true
	}
	return false
}

type fastPacket struct {
	buff *buf.Buffer
	dst  netip.AddrPort
}

var fastPacketPool = sync.Pool{New: func() any { return new(fastPacket) }}

func (p *fastPacket) Data() []byte { return p.buff.Bytes() }

// Drop releases the buffer and recycles the packet; the tunnel calls it exactly once.
func (p *fastPacket) Drop() {
	p.buff.Release()
	p.buff = nil
	fastPacketPool.Put(p)
}

func (p *fastPacket) Destination() netip.AddrPort { return p.dst }

type packet struct {
	writer *network.NetPacketWriter
	mutex  *sync.Mutex
	rAddr  net.Addr
	lAddr  net.Addr
	buff   *buf.Buffer
	reply  *ReplyConn // nil unless the listener batches replies (T-068)
	flow   C.UDPFlow  // fast path of the packet's connection, nil for NewPacket (T-133)
}

// UDPFlow is the fast path the session of this packet takes over.
func (c *packet) UDPFlow() C.UDPFlow { return c.flow }

// ReplyConn returns the listener socket the replies of this packet go out on, if it batches.
func (c *packet) ReplyConn() any {
	if c.reply == nil {
		return nil
	}
	return c.reply
}

func (c *packet) Data() []byte {
	return c.buff.Bytes()
}

// WriteBack wirtes UDP packet with source(ip, port) = `addr`
func (c *packet) WriteBack(b []byte, addr net.Addr) (n int, err error) {
	if addr == nil {
		err = errors.New("address is invalid")
		return
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	conn := *c.writer
	if conn == nil {
		err = errors.New("writeBack to closed connection")
		return
	}

	return conn.WriteTo(b, addr)
}

// LocalAddr returns the source IP/Port of UDP Packet
func (c *packet) LocalAddr() net.Addr {
	return c.rAddr
}

func (c *packet) Drop() {
	c.buff.Release()
}

func (c *packet) InAddr() net.Addr {
	return c.lAddr
}

// WriteBackSource is the writer all datagrams of the packet's connection write back
// through (T-128); NewPacket gives each datagram its own.
func (c *packet) WriteBackSource() any {
	return c.writer
}
