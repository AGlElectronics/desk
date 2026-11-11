// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModuleSettingSecret(t *testing.T) {
	var hits atomic.Int32
	var sawAuth atomic.Value
	sawAuth.Store("")
	password := "s3cret"
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/integrations/bundle" {
			_, _ = w.Write([]byte(`{"brand":{"organization_name":"Fur Con","assets":{}},"settings":{"data":{}}}`))
			return
		}
		hits.Add(1)
		sawAuth.Store(r.Header.Get("Authorization"))
		if r.URL.Path != secretsPath {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer module-cred" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"smtp":{"password":"` + password + `"},"desk":{"imap":{"password":"imap-secret"}}}`))
	}))
	defer den.Close()

	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, fdb)
	ctx := context.Background()

	pw, err := c.SMTPPassword(ctx)
	if err != nil || pw != "s3cret" {
		t.Fatalf("SMTPPassword = %q, %v", pw, err)
	}
	if sawAuth.Load().(string) != "Bearer module-cred" {
		t.Fatalf("authorization %q", sawAuth.Load())
	}
	got, err := c.ModuleSettingSecret(ctx, "desk.imap.password")
	if err != nil || got != "imap-secret" {
		t.Fatalf("nested = %q, %v", got, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("cache missed: %d requests", hits.Load())
	}
	missing, err := c.ModuleSettingSecret(ctx, "smtp.username")
	if err != nil || missing != "" {
		t.Fatalf("absent key = %q, %v", missing, err)
	}

	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	pw, err = c.SMTPPassword(ctx)
	if err != nil || pw != "s3cret" || hits.Load() != 2 {
		t.Fatalf("after sync: %q, %v, hits %d", pw, err, hits.Load())
	}

	password = "rotated"
	c.secretsMu.Lock()
	c.secretAt = time.Now().Add(-SecretCacheTTL - time.Second)
	c.secretsMu.Unlock()
	pw, err = c.SMTPPassword(ctx)
	if err != nil || pw != "rotated" || hits.Load() != 3 {
		t.Fatalf("after expiry: %q, %v, hits %d", pw, err, hits.Load())
	}

	if err := c.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	c.secretsMu.Lock()
	cached := c.secretRaw
	c.secretsMu.Unlock()
	if cached != nil {
		t.Fatal("unpair left the secret in memory")
	}
	if _, err := c.SMTPPassword(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("after unpair: %v", err)
	}
}

func TestModuleSettingSecretStandaloneAndRevoked(t *testing.T) {
	var hits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"smtp":{"password":"s3cret"}}`))
	}))
	defer den.Close()
	ctx := context.Background()

	alone := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, &fakeDB{})
	if _, err := alone.SMTPPassword(ctx); !errors.Is(err, ErrNotPaired) || hits.Load() != 0 {
		t.Fatalf("standalone: %v hits %d", err, hits.Load())
	}
	var nilClient *Client
	if _, err := nilClient.SMTPPassword(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("nil client: %v", err)
	}

	revokedAt := time.Now()
	revoked := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: "module-cred", pairedAt: time.Now(), revokedAt: &revokedAt,
	}})
	if _, err := revoked.SMTPPassword(ctx); !errors.Is(err, ErrRevoked) || hits.Load() != 0 {
		t.Fatalf("revoked: %v hits %d", err, hits.Load())
	}

	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, fdb)
	if _, err := c.SMTPPassword(ctx); err != nil {
		t.Fatal(err)
	}
	fdb.mu.Lock()
	fdb.row.revokedAt = &revokedAt
	fdb.mu.Unlock()
	if _, err := c.SMTPPassword(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked after cache: %v", err)
	}
	c.secretsMu.Lock()
	cached := c.secretRaw
	c.secretsMu.Unlock()
	if cached != nil || hits.Load() != 1 {
		t.Fatalf("revoked call used the cache or Den: cached %d bytes, hits %d", len(cached), hits.Load())
	}
}

func TestModuleSettingSecretUsesDecryptedCredential(t *testing.T) {
	var saw atomic.Value
	saw.Store("")
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"smtp":{"password":"s3cret"}}`))
	}))
	defer den.Close()
	keys, err := newKeyring(testKey(1), "")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keys.seal("module-cred")
	if err != nil {
		t.Fatal(err)
	}
	c := newFakeClient(t, Config{Slug: "stash", EncryptionKey: testKey(1)}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: sealed, pairedAt: time.Now(),
	}})
	pw, err := c.SMTPPassword(context.Background())
	if err != nil || pw != "s3cret" {
		t.Fatalf("%q %v", pw, err)
	}
	if saw.Load().(string) != "Bearer module-cred" {
		t.Fatalf("sent %q", saw.Load())
	}
}

func TestModuleSettingSecretDenErrors(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("unauth")
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "unauth":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid or revoked credential"}}`))
		case "html":
			_, _ = w.Write([]byte(`<html>no</html>`))
		case "number":
			_, _ = w.Write([]byte(`{"smtp":{"password":1}}`))
		case "null":
			_, _ = w.Write([]byte(`{"smtp":{"password":null}}`))
		case "empty":
			_, _ = w.Write([]byte(`{}`))
		case "huge":
			_, _ = w.Write([]byte(`{"smtp":{"password":"` + strings.Repeat("a", secretResponseLimit) + `"}}`))
		default:
			_, _ = w.Write([]byte(`{"smtp":{"password":"s3cret"}}`))
		}
	}))
	defer den.Close()
	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, fdb)

	if _, err := c.SMTPPassword(ctx); !errors.Is(err, ErrRevoked) || !c.Revoked() {
		t.Fatalf("401: %v revoked %v", err, c.Revoked())
	}
	c.secretsMu.Lock()
	if c.secretRaw != nil {
		t.Fatal("401 was cached")
	}
	c.secretsMu.Unlock()

	// The row is revoked now; clear that so the next shapes are attempted.
	fdb.mu.Lock()
	fdb.row.revokedAt = nil
	fdb.mu.Unlock()
	c.setLinked(true, false)

	for _, tc := range []struct {
		mode, key string
		wantErr   string
		wantEmpty bool
	}{
		{"html", "smtp.password", "unexpected secrets", false},
		{"number", "smtp.password", "not a string", false},
		{"number", "smtp", "not a string", false},
		{"null", "smtp.password", "", true},
		{"empty", "smtp.password", "", true},
		{"huge", "smtp.password", "64 KiB", false},
	} {
		mode.Store(tc.mode)
		c.clearSecretCache()
		got, err := c.ModuleSettingSecret(ctx, tc.key)
		if tc.wantEmpty {
			if err != nil || got != "" {
				t.Errorf("%s: %q %v, want empty", tc.mode, got, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: %q %v", tc.mode, got, err)
		}
		// A valid JSON document is cached even when the key is the wrong
		// type. A body that is not a secrets document is not.
		if tc.mode == "number" {
			continue
		}
		c.secretsMu.Lock()
		cached := c.secretRaw != nil
		c.secretsMu.Unlock()
		if cached {
			t.Errorf("%s: error response was cached", tc.mode)
		}
	}

	if _, err := c.ModuleSettingSecret(ctx, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := c.ModuleSettingSecret(ctx, "smtp..password"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("bad key: %v", err)
	}

	// A body of exactly 64 KiB is accepted.
	prefix, suffix := `{"smtp":{"password":"`, `"}}`
	pad := secretResponseLimit - len(prefix) - len(suffix)
	exact := prefix + strings.Repeat("b", pad) + suffix
	if len(exact) != secretResponseLimit {
		t.Fatalf("fixture length %d", len(exact))
	}
	mode.Store("exact")
	den.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(exact))
	})
	c.clearSecretCache()
	got, err := c.SMTPPassword(ctx)
	if err != nil || got != strings.Repeat("b", pad) {
		t.Fatalf("exact limit: %q (%d) %v", got[:min(8, len(got))], len(got), err)
	}
}

func TestModuleSettingSecretDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"smtp":{"password":"stolen"}}`))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+secretsPath, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	c := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: redirector.URL, credential: "module-cred", pairedAt: time.Now(),
	}})
	if _, err := c.SMTPPassword(context.Background()); err == nil || hits.Load() != 0 {
		t.Fatalf("err %v, redirect target hit %d", err, hits.Load())
	}
}

func TestModuleSettingSecretConcurrent(t *testing.T) {
	var hits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"smtp":{"password":"s3cret"}}`))
	}))
	defer den.Close()
	c := newFakeClient(t, Config{Slug: "stash", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: "module-cred", pairedAt: time.Now(),
	}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pw, err := c.SMTPPassword(context.Background())
			if err != nil || pw != "s3cret" {
				t.Errorf("%q %v", pw, err)
			}
		}()
	}
	wg.Wait()
	if hits.Load() == 0 {
		t.Fatal("Den was not asked")
	}
}
