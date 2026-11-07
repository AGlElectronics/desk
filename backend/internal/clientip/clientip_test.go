package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustDefault(t *testing.T) []netip.Prefix {
	t.Helper()
	p, err := ParseCIDRs(DefaultTrustedProxies)
	if err != nil {
		t.Fatalf("ParseCIDRs(default): %v", err)
	}
	return p
}

func TestResolve(t *testing.T) {
	trusted := mustDefault(t)

	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
		wantOK bool
	}{
		// Direct clients: headers are never believed.
		{"untrusted peer, no header", "203.0.113.7:5555", nil, "203.0.113.7", true},
		{"untrusted peer forges XFF", "203.0.113.7:5555", []string{"1.2.3.4"}, "203.0.113.7", true},
		{"untrusted peer forges private XFF", "203.0.113.7:5555", []string{"10.0.0.1"}, "203.0.113.7", true},
		{"untrusted IPv6 peer forges XFF", "[2001:db8::1]:443", []string{"1.2.3.4"}, "2001:db8::1", true},

		// Behind trusted proxies.
		{"trusted peer, no header", "10.0.0.5:1234", nil, "10.0.0.5", true},
		{"single proxy", "10.0.0.5:1234", []string{"198.51.100.9"}, "198.51.100.9", true},
		{"Gateway then nginx", "10.0.1.20:40000", []string{"198.51.100.9, 10.0.2.3"}, "198.51.100.9", true},
		{"client prepends fake entry", "10.0.1.20:40000", []string{"1.2.3.4, 198.51.100.9, 10.0.2.3"}, "198.51.100.9", true},
		{"client prepends many fakes", "127.0.0.1:1", []string{"8.8.8.8,9.9.9.9 , 198.51.100.9,172.18.0.4"}, "198.51.100.9", true},
		{"client fakes private hop", "10.0.1.20:40000", []string{"10.9.9.9, 198.51.100.9, 10.0.2.3"}, "198.51.100.9", true},
		{"multiple XFF headers merged", "10.0.1.20:40000", []string{"1.2.3.4", "198.51.100.9", "10.0.2.3"}, "198.51.100.9", true},
		{"all hops trusted uses leftmost", "10.0.0.5:1234", []string{"192.168.1.10, 10.0.2.3"}, "192.168.1.10", true},
		{"loopback peer", "127.0.0.1:9000", []string{"198.51.100.9"}, "198.51.100.9", true},
		{"rewritten bare peer", "10.0.0.5", []string{"198.51.100.9"}, "198.51.100.9", true},

		// IPv6.
		{"IPv6 proxy chain", "[fd00::5]:8080", []string{"2001:db8::42, fd00::1"}, "2001:db8::42", true},
		{"IPv6 loopback peer", "[::1]:8080", []string{"2001:db8::42"}, "2001:db8::42", true},
		{"IPv4-mapped peer is trusted", "[::ffff:10.0.0.5]:8080", []string{"198.51.100.9"}, "198.51.100.9", true},
		{"IPv4-mapped entry is folded", "10.0.0.5:1", []string{"::ffff:198.51.100.9"}, "198.51.100.9", true},
		{"mapped trusted alias skipped", "10.0.0.5:1", []string{"198.51.100.9, ::ffff:10.0.0.6"}, "198.51.100.9", true},
		{"zone stripped", "10.0.0.5:1", []string{"fe80::1%eth0"}, "fe80::1", true},
		{"entry with port", "10.0.0.5:1", []string{"198.51.100.9:4711"}, "198.51.100.9", true},
		{"bracketed IPv6 entry", "10.0.0.5:1", []string{"[2001:db8::42]"}, "2001:db8::42", true},
		{"bracketed IPv6 entry with port", "10.0.0.5:1", []string{"[2001:db8::42]:443"}, "2001:db8::42", true},

		// Malformed headers.
		{"garbage only", "10.0.0.5:1", []string{"not-an-ip"}, "10.0.0.5", true},
		{"garbage left of real client", "10.0.0.5:1", []string{"garbage, 198.51.100.9"}, "198.51.100.9", true},
		{"garbage behind trusted hop", "10.0.0.5:1", []string{"198.51.100.9, garbage, 10.0.2.3"}, "10.0.2.3", true},
		{"empty entries", "10.0.0.5:1", []string{" , ,198.51.100.9,, "}, "198.51.100.9", true},
		{"empty header", "10.0.0.5:1", []string{""}, "10.0.0.5", true},
		{"unknown token", "10.0.0.5:1", []string{"unknown"}, "10.0.0.5", true},
		{"CIDR in header", "10.0.0.5:1", []string{"198.51.100.0/24"}, "10.0.0.5", true},
		{"unparseable peer", "pipe", []string{"198.51.100.9"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Resolve(tc.remote, tc.xff, trusted)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.String() != tc.want {
				t.Fatalf("Resolve(%q, %q) = %s, want %s", tc.remote, tc.xff, got, tc.want)
			}
		})
	}
}

func TestResolveTrustNone(t *testing.T) {
	trusted, err := ParseCIDRs("none")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := Resolve("10.0.0.5:1", []string{"198.51.100.9"}, trusted)
	if !ok || got.String() != "10.0.0.5" {
		t.Fatalf("got %s, %v; want peer when no proxy is trusted", got, ok)
	}
}

func TestParseCIDRs(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: DefaultTrustedProxies, want: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128", "fc00::/7"}},
		{in: " 203.0.113.0/24 , 2001:db8::/32 ", want: []string{"203.0.113.0/24", "2001:db8::/32"}},
		{in: "203.0.113.9", want: []string{"203.0.113.9/32"}},
		{in: "10.1.2.3/8", want: []string{"10.0.0.0/8"}},
		{in: "none", want: nil},
		{in: "NONE", want: nil},
		{in: "", want: nil},
		{in: "10.0.0.0/33", wantErr: true},
		{in: "10.0.0.0/8,example.com", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseCIDRs(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i].String() != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	trusted := mustDefault(t)
	cases := []struct {
		name       string
		remote     string
		headers    map[string]string
		wantIP     string
		wantRemote string
	}{
		{"direct client forging every header", "203.0.113.7:5555", map[string]string{
			"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "1.2.3.4", "True-Client-IP": "1.2.3.4",
		}, "203.0.113.7", "203.0.113.7"},
		{"proxy with X-Real-IP only is not believed", "10.0.0.5:1234", map[string]string{
			"X-Real-IP": "1.2.3.4", "True-Client-IP": "1.2.3.4",
		}, "10.0.0.5", "10.0.0.5"},
		{"proxy chain", "10.0.0.5:1234", map[string]string{
			"X-Forwarded-For": "1.2.3.4, 198.51.100.9, 10.0.2.3",
		}, "198.51.100.9", "198.51.100.9"},
		{"unparseable peer untouched", "@", map[string]string{
			"X-Forwarded-For": "198.51.100.9",
		}, "@", "@"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotIP, gotRemote string
			h := Middleware(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotIP, gotRemote = FromRequest(r), r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if gotIP != tc.wantIP || gotRemote != tc.wantRemote {
				t.Fatalf("FromRequest = %q, RemoteAddr = %q; want %q, %q", gotIP, gotRemote, tc.wantIP, tc.wantRemote)
			}
		})
	}
}

func TestFromRequestWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := FromRequest(req); got != "203.0.113.7" {
		t.Fatalf("FromRequest = %q, want 203.0.113.7", got)
	}
}
