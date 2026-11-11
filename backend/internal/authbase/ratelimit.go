// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KeyFunc picks the bucket a request counts against. An empty key is not
// limited.
type KeyFunc func(r *http.Request) string

// ClientIP keys by the client address in r.RemoteAddr: an IPv4 address on
// its own ("ip:203.0.113.7"), an IPv6 address by its /64
// ("ip:2001:db8:1:2::/64"), because one subscriber usually gets a whole /64
// and could otherwise pick a fresh address per request. IPv4-mapped IPv6
// addresses count as IPv4. It never reads forwarding headers: behind a
// proxy, mount TrustedProxies.Middleware (Config.TrustedProxies, from
// TRUSTED_PROXY_CIDRS) first, or the backend's own trusted-proxy handling,
// so RemoteAddr is the real client; or use TrustedProxies.ClientIP as the
// key.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "ip:" + host
	}
	return ipKey(addr)
}

func ipKey(addr netip.Addr) string {
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		return "ip:" + netip.PrefixFrom(addr, 64).Masked().String()
	}
	return "ip:" + addr.String()
}

// BySubject keys by the signed-in person when user returns one, and by
// ClientIP otherwise.
func BySubject(user func(r *http.Request) (UserKey, bool)) KeyFunc {
	return func(r *http.Request) string {
		if u, ok := user(r); ok && !u.IsZero() {
			return "sub:" + u.String()
		}
		return ClientIP(r)
	}
}

// RateLimitOptions configures a RateLimiter.
type RateLimitOptions struct {
	// Rate is how many requests per second a key may make on average.
	Rate float64
	// Burst is how many requests a key may make at once (bucket size);
	// at least 1.
	Burst int
	// Key picks the bucket; nil means ClientIP.
	Key KeyFunc
	// IdleTTL drops a key's bucket after this long without requests
	// (default 10 minutes).
	IdleTTL time.Duration
	// MaxKeys bounds memory (default 100000). When full, idle buckets are
	// dropped first, then arbitrary ones.
	MaxKeys int
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter is an in-memory token bucket per key. It is per process: with
// several replicas each one limits on its own. Safe for concurrent use.
type RateLimiter struct {
	o     RateLimitOptions
	mu    sync.Mutex
	keys  map[string]*bucket
	sweep time.Time
}

// NewRateLimiter returns a RateLimiter. It panics on a non-positive Rate.
func NewRateLimiter(o RateLimitOptions) *RateLimiter {
	if o.Rate <= 0 || math.IsNaN(o.Rate) || math.IsInf(o.Rate, 0) {
		panic("authbase: RateLimitOptions.Rate must be positive")
	}
	if o.Burst < 1 {
		o.Burst = 1
	}
	if o.Key == nil {
		o.Key = ClientIP
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = 10 * time.Minute
	}
	if o.MaxKeys <= 0 {
		o.MaxKeys = 100000
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &RateLimiter{o: o, keys: map[string]*bucket{}}
}

// ParseRate parses "N/s", "N/m" or "N/h" (for example "60/m") into
// requests per second.
func ParseRate(s string) (float64, error) {
	n, unit, ok := strings.Cut(strings.TrimSpace(s), "/")
	v, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
	if !ok || err != nil || v <= 0 || math.IsInf(v, 0) {
		return 0, fmt.Errorf("rate %q must look like 10/s, 60/m or 1000/h", s)
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "s":
		return v, nil
	case "m":
		return v / 60, nil
	case "h":
		return v / 3600, nil
	}
	return 0, fmt.Errorf("rate %q must look like 10/s, 60/m or 1000/h", s)
}

// Allow takes a token from key's bucket. When none is left it returns false
// and how long until the next token.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	now := l.o.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= l.o.MaxKeys || now.Sub(l.sweep) > l.o.IdleTTL {
			l.evict(now)
		}
		b = &bucket{tokens: float64(l.o.Burst), last: now}
		l.keys[key] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(float64(l.o.Burst), b.tokens+el*l.o.Rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.o.Rate * float64(time.Second))
	return false, wait
}

func (l *RateLimiter) evict(now time.Time) {
	l.sweep = now
	for k, b := range l.keys {
		if now.Sub(b.last) > l.o.IdleTTL {
			delete(l.keys, k)
		}
	}
	for k := range l.keys {
		if len(l.keys) < l.o.MaxKeys {
			break
		}
		delete(l.keys, k)
	}
}

// Middleware answers 429 rate_limited with Retry-After when the request's
// key is out of tokens.
func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := l.o.Key(r)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := l.Allow(key); !ok {
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": CodeRateLimited, "message": "Too many requests; try again in " + strconv.Itoa(secs) + " seconds.",
			}})
			return
		}
		next.ServeHTTP(w, r)
	})
}
