// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantMode AuthMode
		wantEnv  string
		wantErr  string
		devAuth  bool
	}{
		{"defaults", nil, AuthOIDC, "production", "", false},
		{"dev in development", map[string]string{"AUTH_MODE": "dev", "APP_ENV": "development"}, AuthDev, "development", "", true},
		{"case and spaces", map[string]string{"AUTH_MODE": " DEV ", "APP_ENV": "Development"}, AuthDev, "development", "", true},
		{"dev without APP_ENV", map[string]string{"AUTH_MODE": "dev"}, AuthDev, "production", "only allowed with APP_ENV=development", false},
		{"dev in staging", map[string]string{"AUTH_MODE": "dev", "APP_ENV": "staging"}, AuthDev, "staging", "only allowed", false},
		{"oidc in development", map[string]string{"APP_ENV": "development"}, AuthOIDC, "development", "", false},
		{"unknown mode", map[string]string{"AUTH_MODE": "none"}, "none", "production", "AUTH_MODE must be", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := FromEnv(env(tc.env))
			if c.AuthMode != tc.wantMode || c.AppEnv != tc.wantEnv {
				t.Errorf("got %+v", c)
			}
			if tc.wantErr == "" && err != nil {
				t.Errorf("unexpected error %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
			if c.DevAuth() != tc.devAuth {
				t.Errorf("DevAuth = %v", c.DevAuth())
			}
		})
	}
	if (Config{}).Validate() != nil || (Config{}).DevAuth() {
		t.Error("zero Config must be valid oidc")
	}
	if (Config{AuthMode: AuthDev}).DevAuth() {
		t.Error("dev mode without development must never be DevAuth")
	}
	if !(Config{AuthMode: AuthDev, AppEnv: " Development "}).DevAuth() {
		t.Error("a hand-built Config must normalise APP_ENV too")
	}
}

// The same cases are in denlink's TestNormalizeAppEnv; keep them identical.
func TestNormalizeAppEnv(t *testing.T) {
	for in, want := range map[string]string{
		"":               "production",
		"   ":            "production",
		"development":    "development",
		" Development\n": "development",
		"DEVELOPMENT":    "development",
		"Production":     "production",
		" staging ":      "staging",
	} {
		if got := NormalizeAppEnv(in); got != want {
			t.Errorf("NormalizeAppEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllowedHeaders(t *testing.T) {
	prod := Config{AuthMode: AuthOIDC, AppEnv: "production"}
	dev := Config{AuthMode: AuthDev, AppEnv: "development"}
	if h := strings.Join(prod.AllowedHeaders("X-Extra"), ","); strings.Contains(h, "X-Dev") || !strings.Contains(h, "X-Extra") || !strings.Contains(h, "Authorization") {
		t.Errorf("prod headers %s", h)
	}
	if h := strings.Join(dev.AllowedHeaders(), ","); !strings.Contains(h, HeaderDevUser) || !strings.Contains(h, HeaderDevGroups) {
		t.Errorf("dev headers %s", h)
	}
	if len(BaseAllowedHeaders) != 4 {
		t.Error("AllowedHeaders modified BaseAllowedHeaders")
	}
}

func TestStripDevHeaders(t *testing.T) {
	for _, tc := range []struct {
		cfg  Config
		keep bool
	}{
		{Config{AuthMode: AuthOIDC, AppEnv: "development"}, false},
		{Config{AuthMode: AuthDev, AppEnv: "production"}, false},
		{Config{AuthMode: AuthDev, AppEnv: "development"}, true},
	} {
		var got string
		h := tc.cfg.StripDevHeaders(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get(HeaderDevUser) + r.Header.Get(HeaderDevGroups)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(HeaderDevUser, "alice")
		req.Header.Set(HeaderDevGroups, "den-admin")
		h.ServeHTTP(httptest.NewRecorder(), req)
		if (got != "") != tc.keep {
			t.Errorf("%+v: headers %q", tc.cfg, got)
		}
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	opts := CORSOptions{AllowedOrigins: []string{"https://hr.example.org/", "*"}}
	prod := Config{}.CORS(opts)(next)
	dev := Config{AuthMode: AuthDev, AppEnv: "development"}.CORS(opts)(next)

	preflight := func(h http.Handler, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/api/x", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := preflight(prod, "https://hr.example.org")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://hr.example.org" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed preflight: %d %v", rec.Code, rec.Header())
	}
	if strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "X-Dev") {
		t.Error("prod preflight allows X-Dev headers")
	}
	if !strings.Contains(preflight(dev, "https://hr.example.org").Header().Get("Access-Control-Allow-Headers"), "X-Dev-User") {
		t.Error("dev preflight does not allow X-Dev-User")
	}
	rec = preflight(prod, "https://evil.example.net")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("foreign preflight: %d %v", rec.Code, rec.Header())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://HR.example.org")
	rec = httptest.NewRecorder()
	prod.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "https://HR.example.org" {
		t.Errorf("simple request: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	prod.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("no origin: %d %v", rec.Code, rec.Header())
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	cases := []struct {
		name   string
		body   string
		ct     string
		opts   DecodeOptions
		status int
		msg    string
	}{
		{"ok", `{"name":"a","age":3}`, "application/json", DecodeOptions{}, 0, ""},
		{"unknown field allowed", `{"name":"a","x":1}`, "", DecodeOptions{}, 0, ""},
		{"unknown field rejected", `{"name":"a","x":1}`, "", DecodeOptions{DisallowUnknownFields: true}, 400, `unknown field "x"`},
		{"empty", ``, "", DecodeOptions{}, 400, "empty"},
		{"syntax", `{"name":}`, "", DecodeOptions{}, 400, "not valid JSON"},
		{"truncated", `{"name":"a"`, "", DecodeOptions{}, 400, "ends too early"},
		{"type", `{"age":"x"}`, "", DecodeOptions{}, 400, `field "age" must be int`},
		{"two values", `{"name":"a"} {"name":"b"}`, "", DecodeOptions{}, 400, "single JSON value"},
		{"too large", `{"name":"` + strings.Repeat("a", 100) + `"}`, "", DecodeOptions{MaxBytes: 50}, 413, "larger than 50 bytes"},
		{"too large after value", `{"name":"a"}` + strings.Repeat(" ", 100), "", DecodeOptions{MaxBytes: 50}, 413, "larger than"},
		{"content type ok", `{}`, "application/json; charset=utf-8", DecodeOptions{RequireJSONContentType: true}, 0, ""},
		{"content type +json", `{}`, "application/merge-patch+json", DecodeOptions{RequireJSONContentType: true}, 0, ""},
		{"content type wrong", `{}`, "text/plain", DecodeOptions{RequireJSONContentType: true}, 415, "application/json"},
		{"content type missing", `{}`, "", DecodeOptions{RequireJSONContentType: true}, 415, "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			if tc.ct != "" {
				req.Header.Set("Content-Type", tc.ct)
			}
			rec := httptest.NewRecorder()
			var v in
			err := DecodeJSON(rec, req, &v, tc.opts)
			if tc.status == 0 {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			var de *DecodeError
			if !errors.As(err, &de) || de.Status != tc.status || !strings.Contains(de.Message, tc.msg) {
				t.Fatalf("got %#v, want %d %q", err, tc.status, tc.msg)
			}
			WriteDecodeError(rec, err)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+de.Code+`"`) {
				t.Errorf("written %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	rec := httptest.NewRecorder()
	WriteDecodeError(rec, errors.New("other"))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "bad_request") {
		t.Errorf("generic error: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDecodeJSONDefaultLimit(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", int(DefaultMaxBodyBytes)) + `"}`
	var v struct{ Name string }
	err := DecodeJSON(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big)), &v, DecodeOptions{})
	var de *DecodeError
	if !errors.As(err, &de) || de.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %v", err)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestRateLimiterTokenBucket(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	l := NewRateLimiter(RateLimitOptions{Rate: 1, Burst: 3, Now: clk.now})
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d refused within burst", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("4th request: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other key limited")
	}
	clk.add(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token not refilled after 1s")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("more than one token refilled")
	}
	clk.add(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("bucket not refilled to burst")
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("bucket refilled above burst")
	}
}

func TestRateLimiterEviction(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	l := NewRateLimiter(RateLimitOptions{Rate: 1, Burst: 1, MaxKeys: 3, IdleTTL: time.Minute, Now: clk.now})
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		l.Allow(k)
	}
	if n := len(l.keys); n > 3 {
		t.Fatalf("%d keys kept, max 3", n)
	}
	clk.add(2 * time.Minute)
	l.Allow("f")
	if n := len(l.keys); n != 1 {
		t.Fatalf("%d keys after idle sweep, want 1", n)
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	user := func(r *http.Request) (UserKey, bool) {
		if s := r.Header.Get("X-Test-Sub"); s != "" {
			return UserKey{Issuer: "https://auth.example.org/realms/conservices", Subject: s}, true
		}
		return UserKey{}, false
	}
	l := NewRateLimiter(RateLimitOptions{Rate: 0.5, Burst: 1, Key: BySubject(user), Now: clk.now})
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	do := func(sub, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":5555"
		if sub != "" {
			req.Header.Set("X-Test-Sub", sub)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if do("alice", "10.0.0.1").Code != 200 {
		t.Fatal("first request limited")
	}
	rec := do("alice", "10.0.0.2")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("second request: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if do("bob", "10.0.0.1").Code != 200 {
		t.Fatal("another subject on the same IP was limited")
	}
	if do("", "10.0.0.1").Code != 200 || do("", "10.0.0.1").Code != 429 {
		t.Fatal("anonymous requests are not limited by IP")
	}

	skip := NewRateLimiter(RateLimitOptions{Rate: 1, Key: func(*http.Request) string { return "" }})
	hs := skip.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		hs.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != 200 {
			t.Fatal("empty key was limited")
		}
	}
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"10.1.2.3:443":                 "ip:10.1.2.3",
		"10.1.2.4:443":                 "ip:10.1.2.4",
		"[2001:db8::1]:80":             "ip:2001:db8::/64",
		"[2001:db8:0:0:ffff:1:2:3]:80": "ip:2001:db8::/64",
		"[2001:db8:0:1::1]:80":         "ip:2001:db8:0:1::/64",
		"[::ffff:192.0.2.9]:80":        "ip:192.0.2.9",
		"[fe80::1%eth0]:80":            "ip:fe80::/64",
		"2001:db8:aa:bb:cc:dd:ee:ff":   "ip:2001:db8:aa:bb::/64",
		"weird":                        "ip:weird",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestRateLimiterSharesIPv6Slash64(t *testing.T) {
	l := NewRateLimiter(RateLimitOptions{Rate: 1, Burst: 1, Now: func() time.Time { return time.Unix(0, 0) }})
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	do := func(addr string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if do("[2001:db8:1:2::1]:1000") != 200 {
		t.Fatal("first request limited")
	}
	if do("[2001:db8:1:2::beef]:1001") != http.StatusTooManyRequests {
		t.Error("another address in the same /64 got a bucket of its own")
	}
	if do("[2001:db8:1:3::1]:1000") != 200 {
		t.Error("the next /64 shares the bucket")
	}
	if do("192.0.2.1:1000") != 200 || do("192.0.2.2:1000") != 200 {
		t.Error("IPv4 addresses must be limited one by one")
	}
}

func TestParseRate(t *testing.T) {
	for in, want := range map[string]float64{"10/s": 10, "60/m": 1, " 3600 / h ": 1, "1.5/S": 1.5} {
		if got, err := ParseRate(in); err != nil || got != want {
			t.Errorf("ParseRate(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "10", "0/s", "-1/s", "x/s", "10/d"} {
		if _, err := ParseRate(in); err == nil {
			t.Errorf("ParseRate(%q): want error", in)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("NewRateLimiter with Rate 0 did not panic")
		}
	}()
	NewRateLimiter(RateLimitOptions{})
}

func TestSecurityHeaders(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Referrer-Policy", "same-origin")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("headers %v", rec.Header())
	}
	if rec.Header().Get("Referrer-Policy") != "same-origin" {
		t.Errorf("Referrer-Policy %q, want the handler's own value", rec.Header().Get("Referrer-Policy"))
	}
	rec = httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("default Referrer-Policy %q", rec.Header().Get("Referrer-Policy"))
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS belongs at the Gateway")
	}
}

func TestUserKey(t *testing.T) {
	k := UserKey{Issuer: "https://auth.example.org/realms/conservices", Subject: "a|b"}
	got, ok := ParseUserKey(k.String())
	if !ok || got != k {
		t.Errorf("round trip: %+v %v", got, ok)
	}
	for _, s := range []string{"", "|x", "x|", "noseparator"} {
		if _, ok := ParseUserKey(s); ok {
			t.Errorf("ParseUserKey(%q) ok", s)
		}
	}
	if !(UserKey{Issuer: "x"}).IsZero() || k.IsZero() {
		t.Error("IsZero")
	}
}
