package sing_shadowsocks

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/metacubex/mihomo/adapter/inbound"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/common/sockopt"
	C "github.com/metacubex/mihomo/constant"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/listener/jls"
	"github.com/metacubex/mihomo/listener/restls"
	embedSS "github.com/metacubex/mihomo/listener/shadowsocks"
	"github.com/metacubex/mihomo/listener/shadowtls"
	"github.com/metacubex/mihomo/listener/sing"
	"github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/ntp"
	"github.com/metacubex/mihomo/transport/kcptun"
	obfs "github.com/metacubex/mihomo/transport/simple-obfs"

	shadowsocks "github.com/metacubex/sing-shadowsocks"
	"github.com/metacubex/sing-shadowsocks/shadowaead"
	"github.com/metacubex/sing-shadowsocks/shadowaead_2022"
	"github.com/metacubex/sing/common"
	"github.com/metacubex/sing/common/auth"
	"github.com/metacubex/sing/common/buf"
	"github.com/metacubex/sing/common/bufio"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/metacubex/sing/common/network"
)

// udpReadBatch bounds the datagrams taken per recvmmsg (T-060).
const udpReadBatch = 16

type Listener struct {
	closed       bool
	config       LC.ShadowsocksServer
	listeners    []net.Listener
	udpListeners []net.PacketConn
	service      shadowsocks.Service
	simpleObfs   func(net.Conn) net.Conn
}

var _listener *Listener

func New(config LC.ShadowsocksServer, lc C.InboundListenConfig, tunnel C.Tunnel, additions ...inbound.Addition) (C.MultiAddrListener, error) {
	var sl *Listener
	var err error
	if len(additions) == 0 {
		additions = []inbound.Addition{
			inbound.WithInName("DEFAULT-SHADOWSOCKS"),
			inbound.WithSpecialRules(""),
		}
		defer func() {
			_listener = sl
		}()
	}

	udpTimeout := int64(sing.UDPTimeout.Seconds())

	h, err := sing.NewListenerHandler(sing.ListenerConfig{
		Tunnel:    tunnel,
		Type:      C.SHADOWSOCKS,
		Additions: additions,
		MuxOption: config.MuxOption,
	})
	if err != nil {
		return nil, err
	}

	sl = &Listener{}
	sl.config = config

	switch {
	case config.Cipher == shadowsocks.MethodNone:
		sl.service = shadowsocks.NewNoneService(udpTimeout, h)
	case common.Contains(shadowaead.List, config.Cipher):
		sl.service, err = shadowaead.NewService(config.Cipher, nil, config.Password, udpTimeout, h)
	case common.Contains(shadowaead_2022.List, config.Cipher):
		sl.service, err = shadowaead_2022.NewServiceWithPassword(config.Cipher, config.Password, udpTimeout, h, ntp.Now)
	default:
		err = fmt.Errorf("shadowsocks: unsupported method: %s", config.Cipher)
		return embedSS.New(config, lc, tunnel, additions...)
	}
	if err != nil {
		return nil, err
	}

	securityModes := make([]string, 0, 3)
	if config.ShadowTLS.Enable {
		securityModes = append(securityModes, "shadow-tls")
	}
	if config.ResTLS.Enable {
		securityModes = append(securityModes, "res-tls")
	}
	if config.JLSConfig.Enable {
		securityModes = append(securityModes, "jls")
	}
	if len(securityModes) > 1 {
		return nil, fmt.Errorf("security modes are mutually exclusive: %s", strings.Join(securityModes, ", "))
	}

	var shadowTLSBuilder *shadowtls.Builder
	if config.ShadowTLS.Enable {
		shadowTLSBuilder, err = shadowtls.New(config.ShadowTLS, tunnel)
		if err != nil {
			return nil, err
		}
	}

	var restlsBuilder *restls.Builder
	if config.ResTLS.Enable {
		restlsBuilder = restls.New(config.ResTLS, tunnel)
	}

	var jlsBuilder *jls.Builder
	if config.JLSConfig.Enable {
		jlsBuilder, err = jls.New(config.JLSConfig, tunnel)
		if err != nil {
			return nil, err
		}
	}

	if config.SimpleObfs.Enable {
		switch config.SimpleObfs.Mode {
		case "http":
			sl.simpleObfs = obfs.NewHTTPObfsServer
		case "tls":
			sl.simpleObfs = obfs.NewTLSObfsServer
		default:
			return nil, fmt.Errorf("unsupported simple obfs mode: %s", config.SimpleObfs.Mode)
		}
	}

	var kcptunServer *kcptun.Server
	if config.KcpTun.Enable {
		kcptunServer = kcptun.NewServer(config.KcpTun.Config)
		config.Udp = true
	}

	for _, addr := range strings.Split(config.Listen, ",") {
		addr := addr

		if config.Udp {
			//UDP
			ul, err := lc.ListenPacket(context.Background(), "udp", addr)
			if err != nil {
				return nil, err
			}

			if err := sockopt.UDPReuseaddr(ul); err != nil {
				log.Warnln("Failed to Reuse UDP Address: %s", err)
			}

			sl.udpListeners = append(sl.udpListeners, ul)

			if kcptunServer != nil {
				go kcptunServer.Serve(ul, func(c net.Conn) {
					sl.HandleConn(c, tunnel)
				})

				continue // skip tcp listener
			}

			go func() {
				conn := bufio.NewPacketConn(ul)
				ctx := sing.WithInAddr(context.TODO(), ul.LocalAddr())
				if udp, ok := ul.(*net.UDPConn); ok { // replies go out in batches (T-068)
					rc := sing.NewReplyConn(udp)
					conn, ctx = rc, sing.WithReplyConn(ctx, rc)
				}
				rwOptions := network.NewReadWaitOptions(conn, sl.service)
				// the datagrams already queued are read with one recvmmsg (T-060)
				reader := N.NewBatchReader(ul, udpReadBatch)
				buffs := make([]*buf.Buffer, udpReadBatch)
				bufs := make([][]byte, udpReadBatch)
				defer func() {
					for _, buff := range buffs {
						buff.Release() // nil-safe
					}
				}()
				for {
					for i := range buffs {
						if buffs[i] == nil {
							buffs[i] = rwOptions.NewPacketBuffer()
						}
						bufs[i] = buffs[i].FreeBytes()
					}
					n, err := reader.Read(bufs)
					if err != nil {
						if sl.closed {
							break
						}
						continue
					}
					for i := 0; i < n; i++ {
						buff := buffs[i]
						buffs[i] = nil // handed over to the service
						buff.Truncate(reader.Len(i))
						rwOptions.PostReturn(buff)
						_ = sl.service.NewPacket(ctx, conn, buff, M.Metadata{
							Protocol: "shadowsocks",
							Source:   M.SocksaddrFromNetIP(reader.AddrPort(i)), // no address allocated per datagram (T-128)
						})
					}
				}
			}()
		}

		//TCP
		l, err := lc.Listen(context.Background(), "tcp", addr)
		if err != nil {
			return nil, err
		}
		if shadowTLSBuilder != nil {
			l = shadowTLSBuilder.NewListener(l)
		} else if restlsBuilder != nil {
			l = restlsBuilder.NewListener(l)
		} else if jlsBuilder != nil {
			l = jlsBuilder.NewListener(l)
		}
		sl.listeners = append(sl.listeners, l)

		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					if sl.closed {
						break
					}
					continue
				}

				go sl.HandleConn(c, tunnel)
			}
		}()
	}

	return sl, nil
}

func (l *Listener) Close() error {
	l.closed = true
	var retErr error
	for _, lis := range l.listeners {
		err := lis.Close()
		if err != nil {
			retErr = err
		}
	}
	for _, lis := range l.udpListeners {
		err := lis.Close()
		if err != nil {
			retErr = err
		}
	}
	return retErr
}

func (l *Listener) Config() string {
	return l.config.String()
}

func (l *Listener) AddrList() (addrList []net.Addr) {
	for _, lis := range l.listeners {
		addrList = append(addrList, lis.Addr())
	}
	for _, lis := range l.udpListeners {
		addrList = append(addrList, lis.LocalAddr())
	}
	return
}

func (l *Listener) HandleConn(conn net.Conn, tunnel C.Tunnel, additions ...inbound.Addition) {
	user, loaded := shadowtls.UserFromConn(conn)
	if jlsUser, jlsLoaded := jls.UserFromConn(conn); jlsLoaded {
		user, loaded = jlsUser, true
	}
	if l.simpleObfs != nil {
		conn = l.simpleObfs(conn)
	}
	ctx := sing.WithAdditions(context.TODO(), additions...)
	if loaded {
		ctx = auth.ContextWithUser(ctx, user)
	}
	err := l.service.NewConnection(ctx, conn, M.Metadata{
		Protocol: "shadowsocks",
		Source:   M.SocksaddrFromNet(conn.RemoteAddr()),
	})
	if err != nil {
		_ = conn.Close()
		return
	}
}

func HandleShadowSocks(conn net.Conn, tunnel C.Tunnel, additions ...inbound.Addition) bool {
	if _listener != nil && _listener.service != nil {
		go _listener.HandleConn(conn, tunnel, additions...)
		return true
	}
	return embedSS.HandleShadowSocks(conn, tunnel, additions...)
}
