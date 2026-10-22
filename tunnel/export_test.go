package tunnel

import (
	"net/netip"

	C "github.com/metacubex/mihomo/constant"
)

// RelayReplies runs the reply loop of one UDP session until pc fails, for the external
// test package (T-100): pc is the tracked outbound conn, as handleUDPConn makes it.
func RelayReplies(writeBack C.WriteBack, pc C.PacketConn, key string, from netip.AddrPort) {
	handleUDPToLocal(writeBack, &udpActivityConn{PacketConn: pc}, newPacketSender(), key, from)
}
