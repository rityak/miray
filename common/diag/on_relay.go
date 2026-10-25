//go:build miraydiag

package diag

import (
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/metacubex/mihomo/common/handicap"

	"github.com/metacubex/sing/common"
	"github.com/metacubex/sing/common/bufio"
	"github.com/metacubex/sing/common/network"
)

// Relay is common/net.Relay (the same copy and close calls) that also reports how each
// direction ended. left is the client's connection, right the proxy's.
func Relay(leftConn, rightConn net.Conn) (r RelayResult) {
	leftConn, rightConn = handicap.WrapConn(leftConn), handicap.WrapConn(rightConn)
	defer func() {
		_ = leftConn.Close()
		_ = rightConn.Close()
	}()

	ch := make(chan struct{})
	var downHalf bool
	var downOrder int64 // which direction finished first: the clock may not tell them apart
	go func() {
		_, err := bufio.Copy(leftConn, rightConn)
		r.DownErr, r.DownAt, downOrder = err, time.Now(), order.Add(1)
		if err == nil {
			downHalf = closeWrite(leftConn)
		} else {
			_ = leftConn.Close()
		}
		close(ch)
	}()

	_, err := bufio.Copy(rightConn, leftConn)
	r.UpErr, r.UpAt = err, time.Now()
	upOrder := order.Add(1)
	upHalf := true
	if err == nil {
		upHalf = closeWrite(rightConn)
	} else {
		_ = rightConn.Close()
	}
	<-ch

	r.UpRemote = fromProxy(r.UpErr, leftConn, false)
	r.DownRemote = fromProxy(r.DownErr, leftConn, true)
	// a direction finished cleanly, the other side had no half close and the opposite
	// direction ended in an error after it: it was cut
	r.DownFirst = downOrder < upOrder
	r.FirstEnd = min64(downOrder, upOrder)
	r.UpHalfLost = r.UpErr == nil && !upHalf && r.DownErr != nil && !r.DownFirst
	r.DownHalfLost = r.DownErr == nil && !downHalf && r.UpErr != nil && r.DownFirst
	return r
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// closeWrite is common/net's: CloseWrite when the connection has it, else Close. It
// reports whether the connection was half closed.
func closeWrite(writer io.Closer) bool {
	if c, ok := common.Cast[network.WriteCloser](writer); ok {
		_ = c.CloseWrite()
		return true
	}
	_ = writer.Close()
	return false
}

// fromProxy tells whether err came from the proxy side: a socket error names its
// address, and an error on the client's socket names the client's. sourceIsProxy
// is the direction: the download reads from the proxy, the upload from the client.
func fromProxy(err error, client net.Conn, sourceIsProxy bool) bool {
	if err == nil {
		return false
	}
	// a read on a raw socket (sing's syscall reader, a plain TCP inbound such as the
	// system proxy) carries no address: it failed on the direction's source
	if strings.HasPrefix(err.Error(), "raw read") {
		return sourceIsProxy
	}
	var op *net.OpError
	if errors.As(err, &op) {
		for _, a := range []net.Addr{op.Addr, op.Source} {
			if a == nil {
				continue
			}
			for _, c := range []net.Addr{client.LocalAddr(), client.RemoteAddr()} {
				if c != nil && a.String() == c.String() {
					return false
				}
			}
		}
	}
	return true
}
