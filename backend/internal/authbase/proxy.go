// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// EnvTrustedProxies is the variable FromEnv reads the trusted proxies from.
// Every Con Services deployment already sets it.
const EnvTrustedProxies = "TRUSTED_PROXY_CIDRS"

// DefaultTrustedProxyCIDRs is what FromEnv uses when TRUSTED_PROXY_CIDRS is
// unset: the private and loopback ranges the Gateway, nginx and Caddy run
// in. "none" trusts no proxy.
const DefaultTrustedProxyCIDRs = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7"

// TrustedProxies are the address ranges whose X-Forwarded-For entries are
// believed. The zero value trusts nobody: the connection's address is the
// client.
type TrustedProxies []netip.Prefix

// ParseTrustedProxies reads a comma-separated list of CIDRs ("10.0.0.0/8")
// or single addresses ("203.0.113.5"). Empty and "none" trust nobody.
func ParseTrustedProxies(s string) (TrustedProxies, error) {
	if t := strings.TrimSpace(s); t == "" || strings.EqualFold(t, "none") {
		return nil, nil
	}
	var out TrustedProxies
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a CIDR such as 10.0.0.0/8 or an address", EnvTrustedProxies, part)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Contains reports whether addr is a trusted proxy.
func (t TrustedProxies) Contains(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	for _, p := range t {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientAddr is the address of the client that sent r. When the connection
// comes from a trusted proxy, X-Forwarded-For is read from right to left,
// trusted entries are skipped and the first untrusted one is the client;
// entries a client wrote itself sit further left and are never reached.
// Otherwise the connection's address is the client. X-Real-IP and
// True-Client-IP are never read: a proxy that does not set them would pass
// a forged value through. ok is false when RemoteAddr is not an address.
func (t TrustedProxies) ClientAddr(r *http.Request) (netip.Addr, bool) {
	peer, ok := parseHop(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !t.Contains(peer) {
		return peer, true
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHop(hops[i])
		if !ok {
			break
		}
		client = a
		if !t.Contains(a) {
			break
		}
	}
	return client, true
}

// ClientIP is ClientIP's key (IPv4 address, IPv6 /64) for the address
// ClientAddr finds. Use it as RateLimitOptions.Key, or mount Middleware and
// keep the plain ClientIP.
func (t TrustedProxies) ClientIP(r *http.Request) string {
	if a, ok := t.ClientAddr(r); ok {
		return ipKey(a)
	}
	return ClientIP(r)
}

// Middleware replaces r.RemoteAddr with the address ClientAddr finds, so
// ClientIP, BySubject and request logs see the real client. Mount it first
// on the API router. With no trusted proxies it changes nothing.
func (t TrustedProxies) Middleware(next http.Handler) http.Handler {
	if len(t) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := t.ClientAddr(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		if _, port, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			r2.RemoteAddr = net.JoinHostPort(a.String(), port)
		} else {
			r2.RemoteAddr = a.String()
		}
		next.ServeHTTP(w, r2)
	})
}

// parseHop reads "203.0.113.7", "203.0.113.7:5000", "2001:db8::1" or
// "[2001:db8::1]:443".
func parseHop(s string) (netip.Addr, bool) {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().WithZone(""), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}
