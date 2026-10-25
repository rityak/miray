//go:build !linux

package net

import (
	"net"
	"net/netip"
)

// BatchReader reads one datagram per call off Linux (no recvmmsg).
type BatchReader struct {
	pc   net.PacketConn
	n    int
	addr net.Addr
}

func NewBatchReader(pc net.PacketConn, _ int) *BatchReader { return &BatchReader{pc: pc} }

func (r *BatchReader) Read(bufs [][]byte) (int, error) {
	var err error
	r.n, r.addr, err = r.pc.ReadFrom(bufs[0])
	if err != nil {
		return 0, err
	}
	return 1, nil
}

func (r *BatchReader) Len(int) int       { return r.n }
func (r *BatchReader) Addr(int) net.Addr { return r.addr }

// AddrPort is the source of the datagram, unmapped (T-128).
func (r *BatchReader) AddrPort(int) netip.AddrPort {
	if udp, ok := r.addr.(*net.UDPAddr); ok {
		ap := udp.AddrPort()
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	}
	return netip.AddrPort{}
}
