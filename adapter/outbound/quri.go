package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/metacubex/mihomo/component/ca"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/tls"
	"github.com/rityak/quri/auth"
	QC "github.com/rityak/quri/client"
	QS "github.com/rityak/quri/session"
	QT "github.com/rityak/quri/transport"
)

// QuriOption selects one relay session profile; routing stays in the client core.
type QuriOption struct {
	BasicOption
	Name       string   `proxy:"name"`
	Server     string   `proxy:"server,omitempty"`
	Port       int      `proxy:"port,omitempty"`
	Token      string   `proxy:"token,omitempty"`
	SNI        string   `proxy:"sni,omitempty"`
	Ports      []uint16 `proxy:"ports,omitempty"`
	Transports []string `proxy:"transports,omitempty"`
	Profile    string   `proxy:"profile,omitempty"`
	Upstream   string   `proxy:"upstream,omitempty"`
	CA         string   `proxy:"ca,omitempty"`
}

// Quri owns a lazily established HTTP/3 or HTTP/2 relay client.
type Quri struct {
	*Base
	profile QC.Config
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	client  *QC.Manager
	closed  bool
}

// NewQuri validates a profile without opening sockets or loading server rules.
func NewQuri(option QuriOption) (*Quri, error) {
	var profile QC.Config
	var err error
	if option.Profile != "" {
		if option.Server != "" || option.Token != "" || option.Upstream != "" || option.SNI != "" || option.Port != 0 || len(option.Ports) != 0 || len(option.Transports) != 0 {
			return nil, errors.New("quri profile cannot be combined with connection fields")
		}
		profile, err = QC.ParseLink(option.Profile)
		if err != nil {
			return nil, errors.New("invalid quri profile")
		}
	} else {
		if option.Server == "" || option.Port < 1 || option.Port > 65535 || len(option.Token) < auth.MinTokenSize || len(option.Token) > auth.MaxTokenSize {
			return nil, errors.New("invalid quri server, port or credential")
		}
		serverName := option.SNI
		if serverName == "" {
			serverName = option.Server
		}
		profile = QC.Config{Address: net.JoinHostPort(option.Server, strconv.Itoa(option.Port)), Token: option.Token, Ports: option.Ports, Transports: option.Transports,
			Configuration: QS.Request{Namespace: "direct", Version: 1}, Transport: QT.ClientOptions{TLS: &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS13}}}
		if option.Upstream != "" {
			payload, err := json.Marshal(struct {
				URL string `json:"url"`
			}{option.Upstream})
			if err != nil {
				return nil, err
			}
			profile.Configuration = QS.Request{Namespace: "upstream", Version: 1, Payload: payload}
		}
		if _, err := QC.Link(profile); err != nil {
			return nil, errors.New("invalid quri connection settings")
		}
	}
	profile.Transport.TLS.RootCAs = ca.GetCertPool().Clone()
	if option.CA != "" {
		data := []byte(option.CA)
		if !strings.HasPrefix(option.CA, "-----BEGIN ") {
			data, err = os.ReadFile(option.CA)
			if err != nil {
				return nil, err
			}
		}
		if !profile.Transport.TLS.RootCAs.AppendCertsFromPEM(data) {
			return nil, errors.New("invalid quri CA certificate")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	profile.Ports = append([]uint16(nil), profile.Ports...)
	profile.Transports = append([]string(nil), profile.Transports...)
	q := &Quri{Base: NewBase(BaseOption{Name: option.Name, Addr: profile.Address, Type: C.Quri, UDP: true, ProviderName: option.ProviderName, Interface: option.Interface, RoutingMark: option.RoutingMark, Prefer: option.IPVersion}), profile: profile, ctx: ctx, cancel: cancel}
	q.dialer = option.NewDialer(q.DialOptions())
	q.profile.Transport.DialContext = q.dialer.DialContext
	q.profile.Transport.ListenPacket = func(ctx context.Context, remote netip.AddrPort) (net.PacketConn, error) {
		network := "udp6"
		if remote.Addr().Is4() {
			network = "udp4"
		}
		return q.dialer.ListenPacket(ctx, network, "", remote)
	}
	q.profile.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		switch option.IPVersion {
		case C.IPv4Only:
			return resolver.LookupIPv4(ctx, host)
		case C.IPv6Only:
			return resolver.LookupIPv6(ctx, host)
		default:
			ips, err := resolver.LookupIP(ctx, host)
			if err != nil {
				return nil, err
			}
			ips = append([]netip.Addr(nil), ips...)
			preferV4 := option.IPVersion == C.IPv4Prefer
			sort.SliceStable(ips, func(i, j int) bool { return ips[i].Unmap().Is4() == preferV4 && ips[j].Unmap().Is4() != preferV4 })
			return ips, nil
		}
	}
	return q, nil
}

func (q *Quri) manager(ctx context.Context) (*QC.Manager, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, net.ErrClosed
	}
	if q.client != nil {
		return q.client, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(q.ctx, cancel)
	defer stop()
	client, err := QC.Dial(ctx, q.profile)
	if err != nil {
		return nil, err
	}
	q.client = client
	return client, nil
}

// DialContext implements C.ProxyAdapter with a new HTTP CONNECT stream.
func (q *Quri) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	client, err := q.manager(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := client.DialContext(ctx, metadata.RemoteAddress())
	if err != nil {
		return nil, err
	}
	return NewConn(conn, q), nil
}

// ListenPacketContext preserves an association across relay reconnection.
func (q *Quri) ListenPacketContext(ctx context.Context, _ *C.Metadata) (C.PacketConn, error) {
	client, err := q.manager(ctx)
	if err != nil {
		return nil, err
	}
	packet, err := client.ListenPacket(ctx)
	if err != nil {
		return nil, err
	}
	return NewPacketConn(packet, q), nil
}

// Close cancels establishment before joining any active client worker.
func (q *Quri) Close() error {
	q.cancel()
	q.mu.Lock()
	q.closed = true
	client := q.client
	q.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}
