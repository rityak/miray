//go:build !linux

package tunnel

import (
	"net"

	C "github.com/metacubex/mihomo/constant"
)

type batchWriter struct{}

func newBatchWriter(C.PacketConn) *batchWriter { return nil }

func (*batchWriter) write([][]byte, []*net.UDPAddr) error { return nil }

func trackActivity(*batchWriter, *udpActivityConn) {}
