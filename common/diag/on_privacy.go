//go:build miraydiag

package diag

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/publicsuffix"
)

// dst is a destination as the journal keeps it: the registrable domain (eTLD+1) and
// the port, or a hash of the IP (user decision, S-019).
func dst(host string, ip netip.Addr, port uint16) string {
	p := ":" + strconv.Itoa(int(port))
	if host != "" {
		if _, err := netip.ParseAddr(host); err != nil {
			if d, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(host, ".")); err == nil {
				return d + p
			}
			return "host:" + hashIP(host) + p
		}
		ip, _ = netip.ParseAddr(host)
	}
	if !ip.IsValid() {
		return ""
	}
	return ipName(ip) + p
}

func ipName(ip netip.Addr) string {
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate(), ip.IsLinkLocalUnicast():
		return "lan:" + hashIP(ip.String())
	case ip.Is4():
		return "ip4:" + hashIP(ip.String())
	}
	return "ip6:" + hashIP(ip.String())
}

var (
	reIP6  = regexp.MustCompile(`\[?[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){2,7}(%\w+)?\]?(:\d+)?`)
	reIP4  = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)
	reHost = regexp.MustCompile(`\b([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(:\d+)?\b`)
)

// scrub takes addresses and host names out of an error text: they may name the
// user's servers.
func scrub(s string) string {
	s = reIP4.ReplaceAllString(s, "<ip>")
	s = reIP6.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Count(m, ":") < 2 {
			return m
		}
		return "<ip>"
	})
	s = reHost.ReplaceAllString(s, "<host>")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// class names an error for grouping: timeout, refused, reset, unreachable, eof,
// closed, tls:<alert>, canceled, or other.
func class(err error) string {
	if err == nil {
		return ""
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if c := errnoClass(errno); c != "" {
			return c
		}
	}
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.Is(err, net.ErrClosed), errors.Is(err, io.ErrClosedPipe):
		return "closed"
	}
	s := strings.ToLower(err.Error())
	if i := strings.Index(s, "tls: "); i >= 0 {
		alert := s[i+5:]
		if j := strings.IndexAny(alert, ",;("); j >= 0 {
			alert = alert[:j]
		}
		return "tls:" + strings.TrimSpace(alert)
	}
	switch {
	case strings.Contains(s, "x509:"):
		return "tls:certificate"
	case strings.Contains(s, "timeout"), strings.Contains(s, "timed out"), strings.Contains(s, "deadline"):
		return "timeout"
	case strings.Contains(s, "refused"):
		return "refused"
	case strings.Contains(s, "reset"), strings.Contains(s, "forcibly closed"):
		return "reset"
	case strings.Contains(s, "unreachable"), strings.Contains(s, "no route"):
		return "unreachable"
	case strings.Contains(s, "broken pipe"), strings.Contains(s, "aborted"):
		return "aborted"
	case strings.Contains(s, "no such host"), strings.Contains(s, "no ip"), strings.Contains(s, "dns"):
		return "dns"
	case strings.Contains(s, "closed"):
		return "closed"
	case strings.Contains(s, "eof"):
		return "eof"
	}
	return "other"
}
