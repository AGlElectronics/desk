package probe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestURL(t *testing.T) {
	t.Setenv("TLS_CERT_FILE", "")
	cases := map[string]string{
		":8090":          "http://127.0.0.1:8090/livez",
		"0.0.0.0:8080":   "http://127.0.0.1:8080/livez",
		"[::]:8080":      "http://127.0.0.1:8080/livez",
		"10.0.0.5:9000":  "http://10.0.0.5:9000/livez",
		"localhost:8090": "http://localhost:8090/livez",
	}
	for addr, want := range cases {
		if got, err := URL(addr); err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", addr, got, err, want)
		}
	}
	t.Setenv("TLS_CERT_FILE", "/etc/conservices-tls/tls.crt")
	if got, _ := URL(":8080"); got != "https://127.0.0.1:8080/livez" {
		t.Errorf("tls: %q", got)
	}
	if _, err := URL("8080"); err == nil {
		t.Error("an address without a port must fail")
	}
}

func TestCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer ok.Close()
	if err := Check(ok.URL, time.Second); err != nil {
		t.Fatal(err)
	}
	tlsOK := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer tlsOK.Close()
	if err := Check(tlsOK.URL, time.Second); err != nil {
		t.Fatalf("self-signed loopback TLS must pass: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer bad.Close()
	if err := Check(bad.URL, time.Second); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want 503 error, got %v", err)
	}
}
