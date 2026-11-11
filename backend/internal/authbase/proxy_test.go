// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	tp, err := ParseTrustedProxies(" 10.0.0.0/8, 203.0.113.5 ,2001:db8::/32,, ::ffff:198.51.100.1")
	if err != nil || len(tp) != 4 {
		t.Fatalf("got %v, %v", tp, err)
	}
	for addr, want := range map[string]bool{
		"10.9.8.7": true, "11.0.0.1": false, "203.0.113.5": true, "203.0.113.6": false,
		"2001:db8:1::1": true, "2001:db9::1": false, "198.51.100.1": true, "::ffff:10.1.1.1": true,
	} {
		if got := tp.Contains(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Contains(%s) = %v, want %v", addr, got, want)
		}
	}
	for _, s := range []string{"", "  ", "none", "NONE"} {
		if tp, err := ParseTrustedProxies(s); err != nil || len(tp) != 0 {
			t.Errorf("ParseTrustedProxies(%q) = %v, %v; want nobody", s, tp, err)
		}
	}
	for _, s := range []string{"10.0.0.0/33", "proxy.example.org", "10.0.0.0/8,nope"} {
		if _, err := ParseTrustedProxies(s); err == nil || !strings.Contains(err.Error(), EnvTrustedProxies) {
			t.Errorf("ParseTrustedProxies(%q): err %v", s, err)
		}
	}
	if _, err := ParseTrustedProxies(DefaultTrustedProxyCIDRs); err != nil {
		t.Errorf("default: %v", err)
	}
}

func TestFromEnvTrustedProxies(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil || !c.TrustedProxies.Contains(netip.MustParseAddr("10.1.2.3")) || c.TrustedProxies.Contains(netip.MustParseAddr("8.8.8.8")) {
		t.Fatalf("default: %v %v", c.TrustedProxies, err)
	}
	c, err = FromEnv(env(map[string]string{EnvTrustedProxies: "none"}))
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("none: %v %v", c.TrustedProxies, err)
	}
	if _, err := FromEnv(env(map[string]string{EnvTrustedProxies: "everyone"})); err == nil {
		t.Fatal("bad TRUSTED_PROXY_CIDRS accepted")
	}
	if len((Config{}).TrustedProxies) != 0 {
		t.Fatal("zero Config trusts proxies")
	}
}

func TestClientAddr(t *testing.T) {
	tp, _ := ParseTrustedProxies("10.0.0.0/8,fc00::/7")
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct client, header ignored", "198.51.100.7:4000", []string{"203.0.113.1"}, "198.51.100.7"},
		{"proxy, no header", "10.0.0.2:4000", nil, "10.0.0.2"},
		{"one proxy", "10.0.0.2:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"forged entry left of the real client", "10.0.0.2:4000", []string{"1.2.3.4, 198.51.100.7"}, "198.51.100.7"},
		{"two proxies", "10.0.0.2:4000", []string{"1.2.3.4, 198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"several header lines", "10.0.0.2:4000", []string{"1.2.3.4", "198.51.100.7", "10.0.0.9"}, "198.51.100.7"},
		{"all trusted", "10.0.0.2:4000", []string{"10.0.0.8, 10.0.0.9"}, "10.0.0.8"},
		{"garbage stops at the last good hop", "10.0.0.2:4000", []string{"198.51.100.7, garbage, 10.0.0.9"}, "10.0.0.9"},
		{"entry with port", "10.0.0.2:4000", []string{"198.51.100.7:5555"}, "198.51.100.7"},
		{"ipv6 client behind ipv6 proxy", "[fd00::2]:4000", []string{"[2001:db8::7]:443"}, "2001:db8::7"},
		{"bracketed ipv6", "[fd00::2]:4000", []string{"[2001:db8::8]"}, "2001:db8::8"},
		{"mapped proxy address", "[::ffff:10.0.0.2]:4000", []string{"198.51.100.7"}, "198.51.100.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			r.Header.Set("X-Real-IP", "203.0.113.99")
			got, ok := tp.ClientAddr(r)
			if !ok || got.String() != tc.want {
				t.Errorf("ClientAddr = %v %v, want %s", got, ok, tc.want)
			}
		})
	}

	var nobody TrustedProxies
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got, _ := nobody.ClientAddr(r); got.String() != "10.0.0.2" {
		t.Errorf("no trusted proxies believed X-Forwarded-For: %v", got)
	}
	r.RemoteAddr = "weird"
	if _, ok := tp.ClientAddr(r); ok {
		t.Error("unparsable RemoteAddr ok")
	}
	if got := tp.ClientIP(r); got != "ip:weird" {
		t.Errorf("ClientIP fallback %q", got)
	}
}

func TestTrustedProxiesClientIPAndMiddleware(t *testing.T) {
	tp, _ := ParseTrustedProxies("10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("X-Forwarded-For", "2001:db8:1:2::99")
	if got := tp.ClientIP(r); got != "ip:2001:db8:1:2::/64" {
		t.Errorf("ClientIP = %q", got)
	}

	var seen string
	h := tp.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }))
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.7:4000" || r.RemoteAddr != "10.0.0.2:4000" {
		t.Errorf("middleware: handler saw %q, original now %q", seen, r.RemoteAddr)
	}

	// Rate limits then count the real client, not the proxy.
	l := NewRateLimiter(RateLimitOptions{Rate: 1, Burst: 1})
	limited := tp.Middleware(l.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	do := func(client string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.2:4000"
		r.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, r)
		return rec.Code
	}
	if do("198.51.100.7") != 200 || do("198.51.100.8") != 200 || do("198.51.100.7") != http.StatusTooManyRequests {
		t.Error("clients behind the proxy share a bucket, or one client is not limited")
	}
}

func TestCORSExposeHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	do := func(o CORSOptions, origin string) http.Header {
		o.AllowedOrigins = []string{"https://hr.example.org"}
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		Config{}.CORS(o)(next).ServeHTTP(rec, req)
		return rec.Header()
	}
	if got := do(CORSOptions{}, "https://hr.example.org").Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
		t.Errorf("default expose %q", got)
	}
	if got := do(CORSOptions{ExposeHeaders: []string{"X-Request-ID", "Content-Disposition"}}, "https://hr.example.org").Get("Access-Control-Expose-Headers"); got != "X-Request-ID, Content-Disposition" {
		t.Errorf("custom expose %q", got)
	}
	if h := do(CORSOptions{ExposeHeaders: []string{}}, "https://hr.example.org"); h.Get("Access-Control-Expose-Headers") != "" {
		t.Errorf("empty expose sent %q", h.Get("Access-Control-Expose-Headers"))
	}
	if h := do(CORSOptions{}, "https://evil.example.net"); h.Get("Access-Control-Expose-Headers") != "" {
		t.Error("exposed headers to a foreign origin")
	}
	if h := do(CORSOptions{}, ""); h.Get("Access-Control-Expose-Headers") != "" {
		t.Error("exposed headers without Origin")
	}
}
