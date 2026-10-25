//go:build linux

package net

import (
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// BatchReader reads the datagrams already waiting on a UDP socket with one
// recvmmsg instead of a recvfrom each (T-060). It never waits for more than
// one datagram, so it adds no latency; a sparse flow reads one per call.
//
// The recvmmsg is its own, made from the poller's callback with headers and
// names kept across calls (T-128), as the connected reader of T-096: through
// x/net every call packed all slots and parsed and allocated an address per
// datagram, which cost the ss server more than the batch saved.
type BatchReader struct {
	pc      net.PacketConn
	rawConn syscall.RawConn
	hdrs    []mmsghdr
	iovs    []unix.Iovec
	names   []unix.RawSockaddrInet6 // large enough for an IPv4 name too
	addrs   []netip.AddrPort
	lens    []int
	addr    net.Addr // the one datagram of a socket read with ReadFrom
	bufs    [][]byte // the buffers of the current call, for the callback
	readFn  func(fd uintptr) bool
	rn      int
	rerr    error
}

// mmsghdr is struct mmsghdr of recvmmsg(2); Go pads it as C does on 32 and 64 bits.
type mmsghdr struct {
	hdr unix.Msghdr
	len uint32
}

// NewBatchReader reads up to size datagrams per call from pc; anything other
// than a *net.UDPConn is read one datagram per call.
func NewBatchReader(pc net.PacketConn, size int) *BatchReader {
	r := &BatchReader{pc: pc, addrs: make([]netip.AddrPort, size), lens: make([]int, size)}
	if udp, ok := pc.(*net.UDPConn); ok {
		if rawConn, err := udp.SyscallConn(); err == nil {
			r.rawConn = rawConn
			r.hdrs = make([]mmsghdr, size)
			r.iovs = make([]unix.Iovec, size)
			r.names = make([]unix.RawSockaddrInet6, size)
			r.readFn = r.recv
		}
	}
	return r
}

// Read fills bufs[0:n] with datagrams; Len and AddrPort (or Addr) describe
// datagram i. It blocks until at least one datagram arrives.
func (r *BatchReader) Read(bufs [][]byte) (n int, err error) {
	if r.rawConn == nil {
		var addr net.Addr
		r.lens[0], addr, err = r.pc.ReadFrom(bufs[0])
		if err != nil {
			return 0, err
		}
		r.addr = addr
		r.addrs[0] = addrPortOf(addr)
		return 1, nil
	}
	if len(bufs) > len(r.hdrs) {
		bufs = bufs[:len(r.hdrs)]
	}
	r.bufs, r.rn, r.rerr = bufs, 0, nil
	err = r.rawConn.Read(r.readFn)
	r.bufs = nil
	if err != nil {
		return 0, err
	}
	if r.rerr != nil {
		return 0, r.rerr
	}
	return r.rn, nil
}

// recv is the poller callback of Read: false waits for readability.
func (r *BatchReader) recv(fd uintptr) bool {
	for i, b := range r.bufs {
		r.iovs[i].Base = &b[0]
		r.iovs[i].SetLen(len(b))
		r.hdrs[i] = mmsghdr{}
		r.hdrs[i].hdr.Iov = &r.iovs[i]
		r.hdrs[i].hdr.SetIovlen(1)
		r.hdrs[i].hdr.Name = (*byte)(unsafe.Pointer(&r.names[i]))
		r.hdrs[i].hdr.Namelen = uint32(unsafe.Sizeof(r.names[i]))
	}
	var n uintptr
	var errno syscall.Errno
	for {
		n, _, errno = unix.Syscall6(unix.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&r.hdrs[0])), uintptr(len(r.bufs)), unix.MSG_DONTWAIT, 0, 0)
		if errno != unix.EINTR {
			break
		}
	}
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK {
		return false
	}
	if errno != 0 {
		r.rerr = errno
		return true
	}
	r.rn = int(n)
	for i := 0; i < r.rn; i++ {
		r.lens[i] = int(r.hdrs[i].len)
		r.addrs[i] = sockaddrAddrPort(&r.names[i])
	}
	return true
}

// sockaddrAddrPort reads the source a recvmmsg wrote, IPv4-mapped addresses unmapped.
func sockaddrAddrPort(name *unix.RawSockaddrInet6) netip.AddrPort {
	switch name.Family {
	case unix.AF_INET:
		sa := (*unix.RawSockaddrInet4)(unsafe.Pointer(name))
		p := (*[2]byte)(unsafe.Pointer(&sa.Port))
		return netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), uint16(p[0])<<8|uint16(p[1]))
	case unix.AF_INET6:
		p := (*[2]byte)(unsafe.Pointer(&name.Port))
		addr := netip.AddrFrom16(name.Addr)
		if name.Scope_id != 0 && !addr.Is4In6() {
			addr = addr.WithZone(strconv.FormatUint(uint64(name.Scope_id), 10))
		}
		return netip.AddrPortFrom(addr.Unmap(), uint16(p[0])<<8|uint16(p[1]))
	}
	return netip.AddrPort{}
}

func addrPortOf(addr net.Addr) netip.AddrPort {
	if udp, ok := addr.(*net.UDPAddr); ok {
		ap := udp.AddrPort()
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	}
	return netip.AddrPort{}
}

func (r *BatchReader) Len(i int) int { return r.lens[i] }

// AddrPort is the source of datagram i, without an allocation.
func (r *BatchReader) AddrPort(i int) netip.AddrPort { return r.addrs[i] }

// Addr is the source of datagram i as a net.Addr; it allocates on the recvmmsg path.
func (r *BatchReader) Addr(i int) net.Addr {
	if r.rawConn == nil {
		return r.addr
	}
	return net.UDPAddrFromAddrPort(r.addrs[i])
}
