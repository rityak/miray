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

type corker interface {
	Cork()
	Uncork() [][]byte
}

func newCorkWriter(C.PacketConn) (corker, *batchWriter) { return nil, nil }

type replyHolder interface {
	Hold()
	Release() error
}

func replyBatch(C.WriteBack, *udpActivityConn) (replyHolder, func() int) { return nil, nil }
