// Package tlscert lets a server speak HTTPS with a certificate that is
// re-read from disk every minute, so a certificate renewed by cert-manager is
// used without a restart.
//
// TLS is on when TLS_CERT_FILE and TLS_KEY_FILE are both set. Without them the
// server speaks plain HTTP, which is what the Docker Compose development stack
// uses.
package tlscert

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const reloadInterval = time.Minute

// Configure turns on HTTPS for server when TLS_CERT_FILE and TLS_KEY_FILE are
// set, and keeps re-reading the pair until ctx ends. It reports whether TLS is
// on.
func Configure(ctx context.Context, server *http.Server, logger zerolog.Logger) (bool, error) {
	certFile, keyFile := os.Getenv("TLS_CERT_FILE"), os.Getenv("TLS_KEY_FILE")
	switch {
	case certFile == "" && keyFile == "":
		return false, nil
	case certFile == "" || keyFile == "":
		return false, errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be set together")
	}
	r, err := newReloader(certFile, keyFile)
	if err != nil {
		return false, err
	}
	server.TLSConfig = &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: r.getCertificate,
	}
	go r.watch(ctx, reloadInterval, logger)
	logger.Info().
		Str("cert_file", certFile).
		Time("not_after", r.notAfter()).
		Msg("tls enabled")
	return true, nil
}

// ListenAndServe serves HTTPS when Configure turned TLS on, plain HTTP
// otherwise.
func ListenAndServe(server *http.Server) error {
	if server.TLSConfig != nil {
		return server.ListenAndServeTLS("", "")
	}
	return server.ListenAndServe()
}

type reloader struct {
	certFile, keyFile string

	mu   sync.RWMutex
	cert *tls.Certificate
	raw  []byte
}

func newReloader(certFile, keyFile string) (*reloader, error) {
	r := &reloader{certFile: certFile, keyFile: keyFile}
	if _, err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// reload reads the pair and swaps it in when it changed. A pair that does not
// load (for example read halfway through an update) keeps the previous one.
func (r *reloader) reload() (bool, error) {
	certPEM, err := os.ReadFile(r.certFile)
	if err != nil {
		return false, fmt.Errorf("read TLS_CERT_FILE: %w", err)
	}
	keyPEM, err := os.ReadFile(r.keyFile)
	if err != nil {
		return false, fmt.Errorf("read TLS_KEY_FILE: %w", err)
	}
	raw := append(append([]byte{}, certPEM...), keyPEM...)

	r.mu.RLock()
	unchanged := bytes.Equal(raw, r.raw)
	r.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return false, fmt.Errorf("load TLS certificate %s: %w", r.certFile, err)
	}
	r.mu.Lock()
	r.cert, r.raw = &cert, raw
	r.mu.Unlock()
	return true, nil
}

func (r *reloader) watch(ctx context.Context, every time.Duration, logger zerolog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			changed, err := r.reload()
			if err != nil {
				logger.Warn().Err(err).Msg("tls certificate reload failed; keeping the current one")
				continue
			}
			if changed {
				logger.Info().Time("not_after", r.notAfter()).Msg("tls certificate reloaded")
			}
		}
	}
}

func (r *reloader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

func (r *reloader) notAfter() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cert == nil || r.cert.Leaf == nil {
		return time.Time{}
	}
	return r.cert.Leaf.NotAfter
}
