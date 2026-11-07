// Package clientip works out the real client IP address of a request that
// may have passed through reverse proxies (Gateway, nginx, Caddy).
//
// Forwarding headers are only believed when they were added by a proxy we
// trust: if the TCP peer is not inside one of the trusted CIDRs, the peer
// itself is the client and every header is ignored. Behind trusted proxies,
// X-Forwarded-For is walked from the right (the entry added by the hop
// closest to us) skipping trusted addresses; the first untrusted address is
// the client. True-Client-IP and X-Real-IP are never read: both pass
// through from the client unless every hop strips them.
//
// This package is copied verbatim into every Con Services module; keep the
// copies identical.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies is the default value of TRUSTED_PROXY_CIDRS: the
// private and loopback ranges, where the Gateway, nginx and Caddy run.
const DefaultTrustedProxies = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7"

type ctxKey struct{}

// ParseCIDRs parses a comma-separated list of CIDRs. A bare address is
// read as a single-host prefix. "none" returns an empty list, which trusts
// no proxy at all.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	if strings.EqualFold(strings.TrimSpace(s), "none") {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			addr, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q", part)
			}
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", part)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Resolve returns the client address for a request whose TCP peer is
// remoteAddr and whose X-Forwarded-For header values are xff. It returns
// false only when remoteAddr cannot be parsed.
//
// When the peer is trusted, X-Forwarded-For is walked right to left and the
// first untrusted address wins. If every entry is trusted, the leftmost one
// is used; with no usable entries, the peer. A malformed entry stops the
// walk: nothing left of it can be trusted, so the last good hop is used.
func Resolve(remoteAddr string, xff []string, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parsePeer(remoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !isTrusted(peer, trusted) {
		return peer, true
	}
	client := peer
	for i := len(xff) - 1; i >= 0; i-- {
		entries := strings.Split(xff[i], ",")
		for j := len(entries) - 1; j >= 0; j-- {
			entry := strings.TrimSpace(entries[j])
			if entry == "" {
				continue
			}
			ip, ok := parseEntry(entry)
			if !ok {
				return client, true
			}
			if !isTrusted(ip, trusted) {
				return ip, true
			}
			client = ip
		}
	}
	return client, true
}

// Middleware resolves the client IP once per request, stores it for
// FromRequest and rewrites r.RemoteAddr to it, so request logs show the
// client rather than the last proxy. Requests with an unparseable
// RemoteAddr pass through unchanged.
func Middleware(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip, ok := Resolve(r.RemoteAddr, r.Header.Values("X-Forwarded-For"), trusted); ok {
				s := ip.String()
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, s))
				r.RemoteAddr = s
			}
			next.ServeHTTP(w, r)
		})
	}
}

// FromRequest returns the client IP stored by Middleware. Without the
// middleware it falls back to the host part of r.RemoteAddr and never
// reads forwarding headers.
func FromRequest(r *http.Request) string {
	if s, ok := r.Context().Value(ctxKey{}).(string); ok {
		return s
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	ip = ip.WithZone("")
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// parsePeer parses the host of r.RemoteAddr ("ip:port", or a bare IP once
// an earlier middleware has rewritten it).
func parsePeer(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// parseEntry parses one X-Forwarded-For entry. Besides a plain address it
// accepts "ip:port", "[v6]" and "[v6]:port", which some proxies emit.
// IPv4-mapped IPv6 is folded to IPv4 and zones are dropped, so a client
// cannot write a trusted address in another notation to dodge the check.
func parseEntry(s string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(s)
	if err != nil {
		ap, perr := netip.ParseAddrPort(s)
		switch {
		case perr == nil:
			ip = ap.Addr()
		case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
			if ip, err = netip.ParseAddr(s[1 : len(s)-1]); err != nil {
				return netip.Addr{}, false
			}
		default:
			return netip.Addr{}, false
		}
	}
	return ip.Unmap().WithZone(""), true
}
