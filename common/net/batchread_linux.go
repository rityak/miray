//go:build linux

package net

import (
	"net"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// BatchReader reads the datagrams already waiting on a UDP socket with one
// recvmmsg instead of a recvfrom each (T-060). It never waits for more than
// one datagram, so it adds no latency; a sparse flow reads one per call.
type BatchReader struct {
	pc   net.PacketConn
	v4   *ipv4.PacketConn
	v6   *ipv6.PacketConn
	msgs []ipv4.Message
}

// NewBatchReader reads up to size datagrams per call from pc; anything other
// than a *net.UDPConn is read one datagram per call.
func NewBatchReader(pc net.PacketConn, size int) *BatchReader {
	r := &BatchReader{pc: pc, msgs: make([]ipv4.Message, size)}
	for i := range r.msgs {
		r.msgs[i].Buffers = make([][]byte, 1)
	}
	if udp, ok := pc.(*net.UDPConn); ok {
		if la, ok := udp.LocalAddr().(*net.UDPAddr); ok && la.IP.To4() != nil {
			r.v4 = ipv4.NewPacketConn(udp)
		} else {
			r.v6 = ipv6.NewPacketConn(udp)
		}
	}
	return r
}

// Read fills bufs[0:n] with datagrams; Len and Addr describe datagram i. It
// blocks until at least one datagram arrives.
func (r *BatchReader) Read(bufs [][]byte) (n int, err error) {
	if r.v4 == nil && r.v6 == nil {
		r.msgs[0].N, r.msgs[0].Addr, err = r.pc.ReadFrom(bufs[0])
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	ms := r.msgs
	if len(bufs) < len(ms) {
		ms = ms[:len(bufs)]
	}
	for i := range ms {
		ms[i].Buffers[0] = bufs[i]
	}
	if r.v4 != nil {
		n, err = r.v4.ReadBatch(ms, 0)
	} else {
		n, err = r.v6.ReadBatch(ms, 0)
	}
	if n < 0 { // x/net passes on the -1 of a failed recvmmsg (P-016)
		n = 0
	}
	return n, err
}

func (r *BatchReader) Len(i int) int       { return r.msgs[i].N }
func (r *BatchReader) Addr(i int) net.Addr { return r.msgs[i].Addr }
