package tunnel

import (
	"context"
	"net"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

type trackingListenConfig struct {
	C.InboundListenConfig
	listens int
}

func (c *trackingListenConfig) Listen(context.Context, string, string) (net.Listener, error) {
	c.listens++
	return nil, nil
}

func (c *trackingListenConfig) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	c.listens++
	return nil, nil
}

func TestInvalidTargetDoesNotOpenSocket(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			config := &trackingListenConfig{}
			var err error
			if network == "tcp" {
				_, err = New("127.0.0.1:0", "invalid-target", "", config, nil)
			} else {
				_, err = NewUDP("127.0.0.1:0", "invalid-target", "", config, nil)
			}
			if err == nil || config.listens != 0 {
				t.Fatalf("invalid target: err=%v openedSockets=%d", err, config.listens)
			}
		})
	}
}
