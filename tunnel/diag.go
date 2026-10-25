package tunnel

import (
	"net"

	"github.com/metacubex/mihomo/common/diag"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// Hooks of the diagnostics journal (T-138, S-019); called only under diag.Enabled.

func init() {
	if diag.Enabled {
		diag.SetLookups(proxyType, func() (tcp, udp int) {
			statistic.DefaultManager.Range(func(c statistic.Tracker) bool {
				if c.Info().Metadata.NetWork == C.UDP {
					udp++
				} else {
					tcp++
				}
				return true
			})
			return
		})
	}
}

// proxyType is the adapter type of a proxy or group by name, from the config or a provider.
func proxyType(name string) string {
	if p, ok := proxiesSnapshot()[name]; ok {
		return p.Type().String()
	}
	configMux.RLock()
	defer configMux.RUnlock()
	for _, pd := range providers {
		for _, p := range pd.Proxies() {
			if p.Name() == name {
				return p.Type().String()
			}
		}
	}
	return ""
}

func diagDial(m *C.Metadata, rule C.Rule, proxy C.ProxyAdapter) diag.Dial {
	d := diag.Dial{Network: m.NetWork.String(), Inbound: m.Type.String(), Host: m.Host, IP: m.DstIP, Port: m.DstPort}
	if proxy != nil {
		d.Proxy = proxy.Name()
		// a group's current choice, down to the node: a failure is the node's, not the group's
		func() {
			defer func() { _ = recover() }() // the journal must never take the core down
			var leaf C.ProxyAdapter = proxy
			for i := 0; i < 8; i++ {
				next := leaf.Unwrap(m, false)
				if next == nil {
					break
				}
				leaf = next
			}
			if leaf != proxy {
				d.Via, d.Proxy = proxy.Name(), leaf.Name()
			}
		}()
	}
	if rule != nil {
		d.Rule = rule.RuleType().String()
	}
	return d
}

// diagFlow keys a UDP flow by its source and destination (udp.rebind).
func diagFlow(m *C.Metadata) string { return m.SourceAddress() + ">" + m.RemoteAddress() }

// diagRelay relays a TCP connection and records how it ended.
func diagRelay(conn net.Conn, remoteConn C.Conn) {
	r := diag.Relay(conn, remoteConn)
	t, ok := remoteConn.(statistic.Tracker)
	if !ok {
		return
	}
	info := t.Info()
	var state *diag.ConnState
	if s, ok := remoteConn.(interface{ DiagState() *diag.ConnState }); ok {
		state = s.DiagState()
	}
	m := info.Metadata
	diag.TCPClosed(diag.TCPEnd{
		Dial: diag.Dial{Network: "tcp", Inbound: m.Type.String(), Rule: info.Rule,
			Host: m.Host, IP: m.DstIP, Port: m.DstPort},
		Chain: info.Chain, ID: info.UUID.String(), Start: info.Start,
		Up: info.UploadTotal.Load(), Down: info.DownloadTotal.Load(), State: state, Relay: r,
	})
}

// diagUDPClosed records how a UDP session ended.
func diagUDPClosed(pc *udpActivityConn, sender C.PacketSender) {
	t, ok := pc.PacketConn.(statistic.Tracker)
	if !ok {
		return
	}
	info := t.Info()
	m := info.Metadata
	e := diag.UDPEnd{
		Dial: diag.Dial{Network: "udp", Inbound: m.Type.String(), Rule: info.Rule,
			Host: m.Host, IP: m.DstIP, Port: m.DstPort},
		Chain: info.Chain, Key: diagFlow(m), Start: info.Start,
		Up: info.UploadTotal.Load(), Down: info.DownloadTotal.Load(), Idle: pc.diag.IsIdle(), Conn: &pc.diag,
	}
	if ps, ok := unwrapPacketSender(sender); ok {
		e.Sender = &ps.diag
	}
	diag.UDPClosed(e)
}
