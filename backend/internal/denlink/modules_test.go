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

const modulesBody = `{"modules":[
	{"slug":"desk","display_name":"Desk","base_url":"https://desk.con.example.org","internal_url":"https://desk-backend.conservices-desk.svc.cluster.local:8080","role_catalog_url":"https://desk-backend.conservices-desk.svc.cluster.local:8080/api/role-catalog","enabled":true},
	{"slug":"wiki","display_name":"Wiki","base_url":"https://wiki.con.example.org","internal_url":"","role_catalog_url":""}
]}`

func TestModules(t *testing.T) {
	var hits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/integrations/bundle" {
			_, _ = w.Write([]byte(`{"brand":{"organization_name":"Fur Con","assets":{}},"settings":{"data":{}}}`))
			return
		}
		hits.Add(1)
		if r.URL.Path != modulesPath {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer module-cred" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid or revoked credential"}}`))
			return
		}
		_, _ = w.Write([]byte(modulesBody))
	}))
	defer den.Close()

	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, fdb)
	ctx := context.Background()

	mods, err := c.Modules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 2 || mods[0].Slug != "desk" || mods[0].DisplayName != "Desk" ||
		mods[0].BaseURL != "https://desk.con.example.org" ||
		mods[0].InternalURL != "https://desk-backend.conservices-desk.svc.cluster.local:8080" ||
		mods[0].RoleCatalogURL != "https://desk-backend.conservices-desk.svc.cluster.local:8080/api/role-catalog" {
		t.Fatalf("desk: %+v", mods)
	}
	if mods[1].Slug != "wiki" || mods[1].RoleCatalogURL != "" || mods[1].InternalURL != "" {
		t.Fatalf("wiki: %+v", mods[1])
	}
	mods[0].Slug = "changed"
	again, err := c.Modules(ctx)
	if err != nil || again[0].Slug != "desk" || hits.Load() != 1 {
		t.Fatalf("cache: %+v %v hits %d", again, err, hits.Load())
	}

	c.modulesMu.Lock()
	c.modulesAt = time.Now().Add(-ModulesCacheTTL - time.Second)
	c.modulesMu.Unlock()
	if _, err := c.Modules(ctx); err != nil || hits.Load() != 2 {
		t.Fatalf("after expiry: %v hits %d", err, hits.Load())
	}

	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Modules(ctx); err != nil || hits.Load() != 3 {
		t.Fatalf("after sync: %v hits %d", err, hits.Load())
	}

	if err := c.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	c.modulesMu.Lock()
	cached := c.modulesOK
	c.modulesMu.Unlock()
	if cached {
		t.Fatal("unpair left the module list in memory")
	}
	if _, err := c.Modules(ctx); !errors.Is(err, ErrNotPaired) || hits.Load() != 3 {
		t.Fatalf("after unpair: %v hits %d", err, hits.Load())
	}
}

func TestModulesStandaloneAndRevoked(t *testing.T) {
	var hits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(modulesBody))
	}))
	defer den.Close()
	ctx := context.Background()

	alone := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, &fakeDB{})
	if _, err := alone.Modules(ctx); !errors.Is(err, ErrNotPaired) || hits.Load() != 0 {
		t.Fatalf("standalone: %v hits %d", err, hits.Load())
	}
	var nilClient *Client
	if _, err := nilClient.Modules(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("nil client: %v", err)
	}

	revokedAt := time.Now()
	revoked := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: "module-cred", pairedAt: time.Now(), revokedAt: &revokedAt,
	}})
	if _, err := revoked.Modules(ctx); !errors.Is(err, ErrRevoked) || hits.Load() != 0 {
		t.Fatalf("revoked: %v hits %d", err, hits.Load())
	}

	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, fdb)
	if _, err := c.Modules(ctx); err != nil {
		t.Fatal(err)
	}
	fdb.mu.Lock()
	fdb.row.revokedAt = &revokedAt
	fdb.mu.Unlock()
	if _, err := c.Modules(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked after cache: %v", err)
	}
	c.modulesMu.Lock()
	cached := c.modulesOK
	c.modulesMu.Unlock()
	if cached || hits.Load() != 1 {
		t.Fatalf("revoked call used the cache or Den: cached %v, hits %d", cached, hits.Load())
	}
}

func TestModulesUsesDecryptedCredential(t *testing.T) {
	var saw atomic.Value
	saw.Store("")
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"modules":[]}`))
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
	c := newFakeClient(t, Config{Slug: "hr", EncryptionKey: testKey(1)}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: sealed, pairedAt: time.Now(),
	}})
	mods, err := c.Modules(context.Background())
	if err != nil || len(mods) != 0 {
		t.Fatalf("%+v %v", mods, err)
	}
	if saw.Load().(string) != "Bearer module-cred" {
		t.Fatalf("sent %q", saw.Load())
	}
}

func TestModulesDenErrors(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("unauth")
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "unauth":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid or revoked credential"}}`))
		case "down":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
		case "html":
			_, _ = w.Write([]byte(`<html>no</html>`))
		case "array":
			_, _ = w.Write([]byte(`[]`))
		case "bad-list":
			_, _ = w.Write([]byte(`{"modules":"desk"}`))
		case "empty":
			_, _ = w.Write([]byte(`{}`))
		case "huge":
			_, _ = w.Write([]byte(`{"modules":[{"slug":"` + strings.Repeat("a", modulesResponseLimit) + `"}]}`))
		default:
			_, _ = w.Write([]byte(modulesBody))
		}
	}))
	defer den.Close()
	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "module-cred", pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, fdb)

	if _, err := c.Modules(ctx); !errors.Is(err, ErrRevoked) || !c.Revoked() {
		t.Fatalf("401: %v revoked %v", err, c.Revoked())
	}
	c.modulesMu.Lock()
	if c.modulesOK {
		t.Fatal("401 was cached")
	}
	c.modulesMu.Unlock()

	fdb.mu.Lock()
	fdb.row.revokedAt = nil
	fdb.mu.Unlock()
	c.setLinked(true, false)

	for _, tc := range []struct {
		mode, want string
		empty      bool
	}{
		{"html", "unexpected modules", false},
		{"array", "unexpected modules", false},
		{"bad-list", "unexpected modules", false},
		{"down", "den returned", false},
		{"huge", "64 KiB", false},
		{"empty", "", true},
	} {
		mode.Store(tc.mode)
		c.clearSecretCache()
		mods, err := c.Modules(ctx)
		if tc.empty {
			if err != nil || len(mods) != 0 {
				t.Errorf("%s: %+v %v, want none", tc.mode, mods, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %+v %v", tc.mode, mods, err)
		}
		c.modulesMu.Lock()
		cached := c.modulesOK
		c.modulesMu.Unlock()
		if cached {
			t.Errorf("%s: error response was cached", tc.mode)
		}
	}
}

func TestModulesConcurrent(t *testing.T) {
	var hits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(modulesBody))
	}))
	defer den.Close()
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: den.URL, credential: "module-cred", pairedAt: time.Now(),
	}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mods, err := c.Modules(context.Background())
			if err != nil || len(mods) != 2 || mods[0].RoleCatalogURL == "" {
				t.Errorf("Modules: %+v %v", mods, err)
			}
		}()
	}
	wg.Wait()
	if hits.Load() == 0 {
		t.Fatal("Den was not called")
	}
}
