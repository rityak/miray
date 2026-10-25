package nat

import (
	"net"

	"github.com/metacubex/mihomo/common/atomic"
	C "github.com/metacubex/mihomo/constant"
)

type writeBackProxy struct {
	wb atomic.TypedValue[C.WriteBack]
}

func (w *writeBackProxy) WriteBack(b []byte, addr net.Addr) (n int, err error) {
	return w.wb.Load().WriteBack(b, addr)
}

// ReplyConn passes on the batching listener socket of the current writer (T-068).
func (w *writeBackProxy) ReplyConn() any {
	if r, ok := w.wb.Load().(interface{ ReplyConn() any }); ok {
		return r.ReplyConn()
	}
	return nil
}

// writeBackSource is a WriteBack whose datagrams of one connection share a writer.
type writeBackSource interface{ WriteBackSource() any }

// UpdateWriteBack keeps the current writer when wb writes through the same one (the
// datagrams of a packet connection): storing every datagram's allocated (T-128).
func (w *writeBackProxy) UpdateWriteBack(wb C.WriteBack) {
	if s, ok := wb.(writeBackSource); ok {
		if src := s.WriteBackSource(); src != nil {
			if cur, ok := w.wb.Load().(writeBackSource); ok && cur.WriteBackSource() == src {
				return
			}
		}
	}
	w.wb.Store(wb)
}

func NewWriteBackProxy(wb C.WriteBack) C.WriteBackProxy {
	w := &writeBackProxy{}
	w.UpdateWriteBack(wb)
	return w
}
