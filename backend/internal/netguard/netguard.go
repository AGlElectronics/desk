// Package netguard dials mailbox servers that admins typed in, refusing
// addresses inside the cluster or the host: private, loopback, link-local,
// CGNAT, multicast and unspecified ranges. Without it, a mailbox host such as
// 10.0.0.12:5432 turns "Test connection" into an internal port scanner.
//
// The check runs on every resolved address at connect time (net.Dialer
// Control), so a name that resolves to a public address first and a private
// one later (DNS rebinding) is refused too. On-site mail servers are allowed
// by name or CIDR through DESK_ALLOWED_MAIL_HOSTS.
package netguard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked means the host resolved to an address Desk does not connect to.
var ErrBlocked = errors.New("address is on a private, loopback or link-local network")

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "64:ff9b:1::/48",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Blocked reports whether ip is in a range Desk refuses by default.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Guard decides which mailbox hosts may be dialled.
type Guard struct {
	hosts map[string]struct{}
	cidrs []netip.Prefix
}

// New builds a guard from DESK_ALLOWED_MAIL_HOSTS entries (host names or
// CIDRs; invalid CIDRs were rejected by config validation).
func New(allowed []string) *Guard {
	g := &Guard{hosts: map[string]struct{}{}}
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if p, err := netip.ParsePrefix(a); err == nil {
			g.cidrs = append(g.cidrs, p.Masked())
			continue
		}
		if a != "" {
			g.hosts[strings.TrimSuffix(a, ".")] = struct{}{}
		}
	}
	return g
}

// Permitted reports whether host may connect to ip.
func (g *Guard) Permitted(host string, ip netip.Addr) bool {
	if !Blocked(ip) {
		return true
	}
	if g == nil {
		return false
	}
	if _, ok := g.hosts[strings.TrimSuffix(strings.ToLower(host), ".")]; ok {
		return true
	}
	ip = ip.Unmap()
	for _, p := range g.cidrs {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Dialer returns a net.Dialer for host that refuses forbidden addresses.
func (g *Guard) Dialer(host string, timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return ErrBlocked
			}
			if !g.Permitted(host, ap.Addr()) {
				return ErrBlocked
			}
			return nil
		},
	}
}

// Dial connects to host:port over TCP.
func (g *Guard) Dial(ctx context.Context, host, port string, timeout time.Duration) (net.Conn, error) {
	return g.Dialer(host, timeout).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
}

// DialTLS connects with implicit TLS.
func (g *Guard) DialTLS(ctx context.Context, host, port string, timeout time.Duration, cfg *tls.Config) (net.Conn, error) {
	d := &tls.Dialer{NetDialer: g.Dialer(host, timeout), Config: cfg}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
}

// Describe turns a connection error into a message safe to show to users:
// it says what kind of failure happened without echoing addresses, ports or
// server banners. Log the original error server-side.
func Describe(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	switch {
	case errors.Is(err, ErrBlocked):
		return "this server is on a private or internal network address; an administrator can allow it with DESK_ALLOWED_MAIL_HOSTS"
	case errors.As(err, &dnsErr):
		return "the server name was not found"
	case errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return "could not connect: the server did not answer in time"
	case errors.As(err, &certErr):
		return "secure connection failed: the server's certificate is not valid for this name"
	case errors.As(err, &recordErr):
		return "secure connection failed: the server does not speak TLS on this port (try STARTTLS or another port)"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "could not connect: the server refused the connection"
	}
	return "could not connect to the server"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
