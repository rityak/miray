package dialer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/common/atomic"
	"github.com/metacubex/mihomo/common/diag"
	"github.com/metacubex/mihomo/component/keepalive"
	"github.com/metacubex/mihomo/component/mptcp"
	"github.com/metacubex/mihomo/component/resolver"
)

const (
	DefaultTCPTimeout = 5 * time.Second
	DefaultUDPTimeout = DefaultTCPTimeout

	// connectionAttemptDelay is RFC 8305's Connection Attempt Delay, its recommended value
	connectionAttemptDelay = 250 * time.Millisecond
)

var (
	tcpConcurrent = atomic.NewBool(false)
)

func SetTcpConcurrent(concurrent bool) {
	tcpConcurrent.Store(concurrent)
}

func GetTcpConcurrent() bool {
	return tcpConcurrent.Load()
}

func DialContext(ctx context.Context, network, address string, options ...Option) (conn net.Conn, err error) {
	if diag.Enabled {
		defer func(start time.Time) { diag.Connected(ctx, start, err) }(time.Now())
	}
	opt := applyOptions(options...)

	if opt.network == 4 || opt.network == 6 {
		if strings.Contains(network, "tcp") {
			network = "tcp"
		} else {
			network = "udp"
		}

		network = fmt.Sprintf("%s%d", network, opt.network)
	}

	ips, port, err := parseAddr(ctx, network, address, opt.resolver)
	if err != nil {
		if diag.Enabled {
			diag.ServerDNSFailed(ctx)
		}
		return nil, err
	}
	if ips, err = dropUnspecified(ips); err != nil {
		return nil, err
	}
	switch network {
	case "tcp", "udp", "tcp4", "tcp6", "udp4", "udp6":
		return happyEyeballsDial(ctx, network, happyEyeballsOrder(ips, opt.prefer), port, opt)
	default:
		return nil, ErrorInvalidedNetworkStack
	}
}

func ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort, options ...Option) (net.PacketConn, error) {
	opt := applyOptions(options...)

	lc, address, err := listenConfig(network, address, rAddrPort, opt)
	if err != nil {
		return nil, err
	}
	return lc.ListenPacket(ctx, network, address)
}

// Listen creates a TCP listener with the same socket policy as ListenPacket.
func Listen(ctx context.Context, network, address string, options ...Option) (net.Listener, error) {
	lc, address, err := listenConfig(network, address, netip.AddrPort{}, applyOptions(options...))
	if err != nil {
		return nil, err
	}
	return lc.Listen(ctx, network, address)
}

func listenConfig(network, address string, rAddrPort netip.AddrPort, opt option) (*net.ListenConfig, string, error) {
	lc := &net.ListenConfig{}
	if opt.addrReuse {
		addrReuseToListenConfig(lc)
	}
	if DefaultSocketHook != nil { // ignore interfaceName, routingMark when DefaultSocketHook not null (in CMFA)
		socketHookToListenConfig(lc)
	} else {
		if opt.interfaceName == "" {
			opt.interfaceName = DefaultInterface.Load()
		}
		if opt.interfaceName == "" {
			if finder := DefaultInterfaceFinder.Load(); finder != nil {
				opt.interfaceName = finder.FindInterfaceName(rAddrPort.Addr().Unmap())
			}
		}
		if rAddrPort.Addr().Unmap().IsLoopback() || listenAddressIsLoopback(address) {
			// avoid "The requested address is not valid in its context."
			opt.interfaceName = ""
		}
		if opt.interfaceName != "" {
			bind := bindIfaceToListenConfig
			if opt.fallbackBind {
				bind = fallbackBindIfaceToListenConfig
			}
			addr, err := bind(opt.interfaceName, lc, network, address, rAddrPort)
			if err != nil {
				return nil, "", err
			}
			address = addr
		}
		if opt.routingMark == 0 {
			opt.routingMark = int(DefaultRoutingMark.Load())
		}
		if opt.routingMark != 0 {
			bindMarkToListenConfig(opt.routingMark, lc, network, address)
		}
	}

	return lc, address, nil
}

func dialContext(ctx context.Context, network string, destination netip.Addr, port string, opt option) (net.Conn, error) {
	var address string
	destination, port = resolver.LookupIP4P(destination, port)
	address = net.JoinHostPort(destination.String(), port)

	netDialer := opt.netDialer
	switch netDialer.(type) {
	case nil:
		netDialer = &net.Dialer{}
	case *net.Dialer:
		_netDialer := *netDialer.(*net.Dialer)
		netDialer = &_netDialer // make a copy
	default:
		return netDialer.DialContext(ctx, network, address)
	}

	dialer := netDialer.(*net.Dialer)
	keepalive.SetNetDialer(dialer)
	mptcp.SetNetDialer(dialer, opt.mpTcp)

	if DefaultSocketHook != nil { // ignore interfaceName, routingMark and tfo when DefaultSocketHook not null (in CMFA)
		socketHookToToDialer(dialer)
	} else {
		if opt.interfaceName == "" {
			opt.interfaceName = DefaultInterface.Load()
		}
		if opt.interfaceName == "" {
			if finder := DefaultInterfaceFinder.Load(); finder != nil {
				opt.interfaceName = finder.FindInterfaceName(destination)
			}
		}
		if opt.interfaceName != "" {
			bind := bindIfaceToDialer
			if opt.fallbackBind {
				bind = fallbackBindIfaceToDialer
			}
			if err := bind(opt.interfaceName, dialer, network, destination); err != nil {
				return nil, err
			}
		}
		if opt.routingMark == 0 {
			opt.routingMark = int(DefaultRoutingMark.Load())
		}
		if opt.routingMark != 0 {
			bindMarkToDialer(opt.routingMark, dialer, network, destination)
		}
		if opt.tfo && !DisableTFO {
			return dialTFO(ctx, *dialer, network, address)
		}
	}

	return dialer.DialContext(ctx, network, address)
}

func ICMPControl(destination netip.Addr) func(network, address string, conn syscall.RawConn) error {
	return func(network, address string, conn syscall.RawConn) error {
		if DefaultSocketHook != nil {
			return DefaultSocketHook(network, address, conn)
		}
		dialer := &net.Dialer{}
		interfaceName := DefaultInterface.Load()
		if interfaceName == "" {
			if finder := DefaultInterfaceFinder.Load(); finder != nil {
				interfaceName = finder.FindInterfaceName(destination)
			}
		}
		if interfaceName != "" {
			if err := bindIfaceToDialer(interfaceName, dialer, network, destination); err != nil {
				return err
			}
		}
		routingMark := int(DefaultRoutingMark.Load())
		if routingMark != 0 {
			bindMarkToDialer(routingMark, dialer, network, destination)
		}
		if dialer.ControlContext != nil {
			return dialer.ControlContext(context.TODO(), network, address, conn)
		}
		return nil
	}
}

type dialFunc func(ctx context.Context, network string, ips []netip.Addr, port string, opt option) (net.Conn, error)

// happyEyeballsOrder interleaves the families, the preferred one first, each in
// its own order (RFC 8305 §4, First Address Family Count 1): IPv6 first unless
// IPv4 is preferred.
func happyEyeballsOrder(ips []netip.Addr, prefer int) []netip.Addr {
	ipv4s, ipv6s := resolver.SortationAddr(ips)
	first, second := ipv6s, ipv4s
	if prefer == 4 {
		first, second = ipv4s, ipv6s
	}
	ordered := make([]netip.Addr, 0, len(ips))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			ordered = append(ordered, first[i])
		}
		if i < len(second) {
			ordered = append(ordered, second[i])
		}
	}
	return ordered
}

// happyEyeballsDial tries the addresses in order, each attempt
// connectionAttemptDelay after the previous one or at once when it fails; the
// first connection wins, the attempts still running are canceled (RFC 8305 §5,
// T-155). Before, the addresses of a family went one after another, each with a
// share of the timeout, and both families at once. A custom dialer keeps the
// caller's context, as it may bind the stream to it: its late connections are
// closed instead.
func happyEyeballsDial(ctx context.Context, network string, ips []netip.Addr, port string, opt option) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, ErrorNoIpAddress
	}
	if len(ips) == 1 {
		return dialContext(ctx, network, ips[0], port, opt)
	}
	switch opt.netDialer.(type) {
	case nil, *net.Dialer:
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	results := make(chan dialResult, len(ips)) // buffered: an attempt never blocks after we returned
	next, pending := 0, 0
	start := func() {
		ip := ips[next]
		next++
		pending++
		go func() {
			conn, err := dialContext(ctx, network, ip, port, opt)
			results <- dialResult{ip: ip, Conn: conn, error: err}
		}()
	}
	start()
	timer := time.NewTimer(connectionAttemptDelay)
	defer timer.Stop()
	var errs []error
	for pending > 0 {
		select {
		case <-timer.C:
			if next < len(ips) {
				start()
				timer.Reset(connectionAttemptDelay)
			}
		case res := <-results:
			pending--
			if res.error == nil {
				if pending > 0 {
					go func(n int) {
						for i := 0; i < n; i++ {
							if late := <-results; late.error == nil {
								_ = late.Conn.Close()
							}
						}
					}(pending)
				}
				return res.Conn, nil
			}
			errs = append(errs, fmt.Errorf("connect failed: %w", res.error))
			if next < len(ips) {
				start()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(connectionAttemptDelay)
			}
		}
	}
	return nil, errors.Join(errs...)
}

type dialResult struct {
	ip netip.Addr
	net.Conn
	error
}

func parseAddr(ctx context.Context, network, address string, preferResolver resolver.Resolver) ([]netip.Addr, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, "-1", err
	}

	if preferResolver == nil {
		preferResolver = resolver.ProxyServerHostResolver
	}

	var ips []netip.Addr
	switch network {
	case "tcp4", "udp4":
		ips, err = resolver.LookupIPv4WithResolver(ctx, host, preferResolver)
	case "tcp6", "udp6":
		ips, err = resolver.LookupIPv6WithResolver(ctx, host, preferResolver)
	default:
		ips, err = resolver.LookupIPWithResolver(ctx, host, preferResolver)
	}
	if err != nil {
		return nil, "-1", fmt.Errorf("dns resolve failed: %w", err)
	}
	for i, ip := range ips {
		if ip.Is4In6() {
			ips[i] = ip.Unmap()
		}
	}
	return ips, port, nil
}

func listenAddressIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

type Dialer struct {
	Opt option
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return DialContext(ctx, network, address, WithOption(d.Opt))
}

func (d Dialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	return ListenPacket(ctx, ParseNetwork(network, rAddrPort.Addr()), address, rAddrPort, WithOption(d.Opt))
}

func NewDialer(options ...Option) Dialer {
	opt := applyOptions(options...)
	return Dialer{Opt: opt}
}
