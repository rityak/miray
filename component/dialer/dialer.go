package dialer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
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

	dualStackFallbackTimeout = 300 * time.Millisecond
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
	// shortcut: custom dialers retain their context; audit stream lifetimes before canceling their pending dials.
	switch opt.netDialer.(type) {
	case nil, *net.Dialer:
		if len(ips) > 1 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
		}
	}

	tcpConcurrent := GetTcpConcurrent()

	switch network {
	case "tcp4", "tcp6", "udp4", "udp6":
		if tcpConcurrent {
			return parallelDialContext(ctx, network, ips, port, opt)
		}
		return serialDialContext(ctx, network, ips, port, opt)
	case "tcp", "udp":
		if tcpConcurrent {
			if opt.prefer != 4 && opt.prefer != 6 {
				return parallelDialContext(ctx, network, ips, port, opt)
			}
			return dualStackDialContext(ctx, parallelDialContext, network, ips, port, opt)
		}
		return dualStackDialContext(ctx, serialDialContext, network, ips, port, opt)
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

func dualStackDialContext(ctx context.Context, dialFn dialFunc, network string, ips []netip.Addr, port string, opt option) (net.Conn, error) {
	ipv4s, ipv6s := resolver.SortationAddr(ips)
	if len(ipv4s) == 0 && len(ipv6s) == 0 {
		return nil, ErrorNoIpAddress
	}
	if len(ipv4s) == 0 && len(ipv6s) != 0 {
		return dialFn(ctx, network, ipv6s, port, opt)
	}
	if len(ipv4s) != 0 && len(ipv6s) == 0 {
		return dialFn(ctx, network, ipv4s, port, opt)
	}

	preferIPVersion := opt.prefer
	switch preferIPVersion {
	case 4:
		return staggeredDialContext(ctx, dialFn, network, ipv4s, ipv6s, port, opt)
	case 6:
		return staggeredDialContext(ctx, dialFn, network, ipv6s, ipv4s, port, opt)
	}
	fallbackTicker := time.NewTicker(dualStackFallbackTimeout)
	defer fallbackTicker.Stop()

	results := make(chan dialResult)
	returned := make(chan struct{})
	defer close(returned)

	var wg sync.WaitGroup

	racer := func(ips []netip.Addr, isPrimary bool) {
		defer wg.Done()
		result := dialResult{isPrimary: isPrimary}
		defer func() {
			select {
			case results <- result:
			case <-returned:
				if result.Conn != nil && result.error == nil {
					_ = result.Conn.Close()
				}
			}
		}()
		result.Conn, result.error = dialFn(ctx, network, ips, port, opt)
	}

	if len(ipv4s) != 0 {
		wg.Add(1)
		go racer(ipv4s, preferIPVersion != 6)
	}

	if len(ipv6s) != 0 {
		wg.Add(1)
		go racer(ipv6s, preferIPVersion != 4)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var fallback dialResult
	var errs []error

loop:
	for {
		select {
		case <-fallbackTicker.C:
			if fallback.error == nil && fallback.Conn != nil {
				return fallback.Conn, nil
			}
		case res, ok := <-results:
			if !ok {
				break loop
			}
			if res.error == nil {
				if res.isPrimary {
					if fallback.error == nil && fallback.Conn != nil {
						go func() { // close fallback connection in new goroutine to avoid blocking
							_ = fallback.Conn.Close()
						}()
					}
					return res.Conn, nil
				}
				fallback = res
			} else {
				if res.isPrimary {
					errs = append([]error{fmt.Errorf("connect failed: %w", res.error)}, errs...)
				} else {
					errs = append(errs, fmt.Errorf("connect failed: %w", res.error))
				}
			}
		}
	}

	if fallback.error == nil && fallback.Conn != nil {
		return fallback.Conn, nil
	}
	return nil, errors.Join(errs...)
}

// staggeredDialContext dials the preferred family first and starts the other
// one only after dualStackFallbackTimeout or a failure of the first (RFC 8305),
// instead of two connections for every dial. The first success wins; the
// other attempt is canceled for a standard net.Dialer, and a late connection
// is closed (T-011).
func staggeredDialContext(ctx context.Context, dialFn dialFunc, network string, primary, fallback []netip.Addr, port string, opt option) (net.Conn, error) {
	switch opt.netDialer.(type) {
	case nil, *net.Dialer:
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	results := make(chan dialResult, 2) // buffered: an attempt never blocks after we returned
	start := func(ips []netip.Addr, isPrimary bool) {
		go func() {
			result := dialResult{isPrimary: isPrimary}
			result.Conn, result.error = dialFn(ctx, network, ips, port, opt)
			results <- result
		}()
	}
	start(primary, true)
	timer := time.NewTimer(dualStackFallbackTimeout)
	defer timer.Stop()

	pending, fallbackStarted := 1, false
	startFallback := func() {
		if !fallbackStarted {
			fallbackStarted = true
			pending++
			start(fallback, false)
		}
	}
	var errs []error
	for pending > 0 {
		select {
		case <-timer.C:
			startFallback()
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
			if res.isPrimary {
				errs = append([]error{fmt.Errorf("connect failed: %w", res.error)}, errs...)
			} else {
				errs = append(errs, fmt.Errorf("connect failed: %w", res.error))
			}
			startFallback()
		}
	}
	return nil, errors.Join(errs...)
}

func parallelDialContext(ctx context.Context, network string, ips []netip.Addr, port string, opt option) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, ErrorNoIpAddress
	}
	if len(ips) == 1 {
		return dialContext(ctx, network, ips[0], port, opt)
	}
	results := make(chan dialResult)
	returned := make(chan struct{})
	defer close(returned)
	racer := func(ctx context.Context, ip netip.Addr) {
		result := dialResult{isPrimary: true, ip: ip}
		defer func() {
			select {
			case results <- result:
			case <-returned:
				if result.Conn != nil && result.error == nil {
					_ = result.Conn.Close()
				}
			}
		}()
		result.Conn, result.error = dialContext(ctx, network, ip, port, opt)
	}

	for _, ip := range ips {
		go racer(ctx, ip)
	}
	var errs []error
	for i := 0; i < len(ips); i++ {
		res := <-results
		if res.error == nil {
			return res.Conn, nil
		}
		errs = append(errs, res.error)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, os.ErrDeadlineExceeded
}

func serialDialContext(ctx context.Context, network string, ips []netip.Addr, port string, opt option) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, ErrorNoIpAddress
	}
	var errs []error
	for i, ip := range ips {
		if conn, err := dialAttempt(ctx, network, ip, port, opt, len(ips)-i); err == nil {
			return conn, nil
		} else {
			errs = append(errs, err)
		}
	}
	return nil, errors.Join(errs...)
}

// dialAttempt dials one of the remaining addresses. With a deadline and more
// addresses left, the attempt gets an equal share of the remaining budget, so a
// blackholed first address cannot use it all (T-011). Only a standard
// net.Dialer gets the shorter context: a custom dialer may bind the returned
// stream to it.
func dialAttempt(ctx context.Context, network string, ip netip.Addr, port string, opt option, remaining int) (net.Conn, error) {
	deadline, ok := ctx.Deadline()
	if !ok || remaining <= 1 {
		return dialContext(ctx, network, ip, port, opt)
	}
	switch opt.netDialer.(type) {
	case nil, *net.Dialer:
	default:
		return dialContext(ctx, network, ip, port, opt)
	}
	attemptCtx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Until(deadline)/time.Duration(remaining)))
	defer cancel() // an established net.Conn is not affected by its dial context
	return dialContext(attemptCtx, network, ip, port, opt)
}

type dialResult struct {
	ip netip.Addr
	net.Conn
	error
	isPrimary bool
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
