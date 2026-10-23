package common

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// enableICMPErrors makes the kernel report ICMP errors on the client's unconnected UDP socket, as it does
// for a connected one: a closed server port (the server restarted) then ends the QUIC transport at once
// instead of after the idle timeout (T-116). quic-go keeps reading on the soft ones (packet too big,
// unreachable).
func enableICMPErrors(pc net.PacketConn) {
	sc, ok := pc.(syscall.Conn)
	if !ok {
		return
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_RECVERR, 1)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVERR, 1)
	})
}
