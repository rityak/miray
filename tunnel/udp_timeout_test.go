package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/net/deadline"
	"github.com/metacubex/mihomo/common/net/packet"
	"github.com/metacubex/mihomo/component/dialer"
	C "github.com/metacubex/mihomo/constant"
)

// timeoutPC adapts an EnhancePacketConn to C.PacketConn for handleUDPToLocal.
type timeoutPC struct {
	C.PacketConn
	epc packet.EnhancePacketConn
}

func (c *timeoutPC) WaitReadFrom() ([]byte, func(), net.Addr, error) { return c.epc.WaitReadFrom() }
func (c *timeoutPC) WriteTo(b []byte, a net.Addr) (int, error)       { return c.epc.WriteTo(b, a) }
func (c *timeoutPC) SetReadDeadline(t time.Time) error               { return c.epc.SetReadDeadline(t) }
func (c *timeoutPC) Close() error                                    { return c.epc.Close() }

type nopWriteBack struct{}

func (nopWriteBack) WriteBack(b []byte, _ net.Addr) (int, error) { return len(b), nil }

// The session must survive while only outbound writes happen (no replies), and
// close after udpTimeout of no activity in either direction (T-007).
func TestUDPIdleTimeoutLazyDeadline(t *testing.T) {
	for name, wrap := range map[string]func(net.PacketConn) packet.EnhancePacketConn{
		"socket": packet.NewEnhancePacketConn,
		"deadline": func(pc net.PacketConn) packet.EnhancePacketConn {
			return deadline.NewEnhancePacketConn(packet.NewEnhancePacketConn(pc))
		},
	} {
		t.Run(name, func(t *testing.T) {
			old := udpTimeout.Load()
			udpTimeout.Store(int64(200 * time.Millisecond))
			defer udpTimeout.Store(old)

			udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) // never replies
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()

			pc := &udpActivityConn{PacketConn: &timeoutPC{epc: wrap(udp)}}
			done := make(chan struct{})
			go func() {
				handleUDPToLocal(nopWriteBack{}, pc, newPacketSender(), "timeout-test", netip.AddrPort{})
				close(done)
			}()

			for i := 0; i < 10; i++ { // 1 s of writes = 5 idle intervals
				if _, err := pc.WriteTo([]byte{1}, sink.LocalAddr()); err != nil {
					t.Fatal(err)
				}
				time.Sleep(100 * time.Millisecond)
				select {
				case <-done:
					t.Fatalf("session closed after %d writes while active", i+1)
				default:
				}
			}
			select {
			case <-done:
			case <-time.After(3 * 200 * time.Millisecond):
				t.Fatal("session not closed after becoming idle")
			}
		})
	}
}

// A DNS-blocked destination fails its dial once: retry stops on it (T-146).
func TestRetryStopsOnUnspecifiedDestination(t *testing.T) {
	calls := 0
	_, err := retry(context.Background(), func(context.Context) (int, error) {
		calls++
		return 0, fmt.Errorf("dial: %w", dialer.ErrUnspecifiedDestination)
	}, nil)
	if !errors.Is(err, dialer.ErrUnspecifiedDestination) || calls != 1 {
		t.Fatalf("%d attempts, %v; want 1 and ErrUnspecifiedDestination", calls, err)
	}
}
