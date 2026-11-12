package tlscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func writePair(t *testing.T, dir, cn string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func commonName(t *testing.T, r *reloader) string {
	t.Helper()
	c, err := r.getCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Leaf.Subject.CommonName
}

func TestReloadPicksUpRenewedCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "first")
	r, err := newReloader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := commonName(t, r); got != "first" {
		t.Fatalf("got %q, want first", got)
	}

	if changed, err := r.reload(); err != nil || changed {
		t.Fatalf("unchanged files: changed=%v err=%v", changed, err)
	}

	writePair(t, dir, "second")
	if changed, err := r.reload(); err != nil || !changed {
		t.Fatalf("renewed files: changed=%v err=%v", changed, err)
	}
	if got := commonName(t, r); got != "second" {
		t.Fatalf("got %q, want second", got)
	}
}

func TestReloadKeepsCurrentCertificateOnBrokenPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, "good")
	r, err := newReloader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("half written"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.reload(); err == nil {
		t.Fatal("expected an error for a broken key")
	}
	if got := commonName(t, r); got != "good" {
		t.Fatalf("got %q, want good", got)
	}
}

func TestConfigure(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("off without files", func(t *testing.T) {
		t.Setenv("TLS_CERT_FILE", "")
		t.Setenv("TLS_KEY_FILE", "")
		srv := &http.Server{}
		on, err := Configure(context.Background(), srv, logger)
		if err != nil || on || srv.TLSConfig != nil {
			t.Fatalf("on=%v err=%v tls=%v", on, err, srv.TLSConfig)
		}
	})

	t.Run("one file is an error", func(t *testing.T) {
		t.Setenv("TLS_CERT_FILE", "/tls/tls.crt")
		t.Setenv("TLS_KEY_FILE", "")
		if _, err := Configure(context.Background(), &http.Server{}, logger); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("serves https", func(t *testing.T) {
		certFile, keyFile := writePair(t, t.TempDir(), "served")
		t.Setenv("TLS_CERT_FILE", certFile)
		t.Setenv("TLS_KEY_FILE", keyFile)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if on, err := Configure(ctx, srv, logger); err != nil || !on {
			t.Fatalf("on=%v err=%v", on, err)
		}
		go func() { _ = srv.ServeTLS(ln, "", "") }()
		defer func() { _ = srv.Close() }()

		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test certificate
		}}
		resp, err := client.Get("https://" + ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if cn := resp.TLS.PeerCertificates[0].Subject.CommonName; cn != "served" {
			t.Fatalf("served certificate %q, want served", cn)
		}
	})
}
