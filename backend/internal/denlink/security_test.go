// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"
)

func testKey(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

// fakeDB keeps the single den_link row in memory and understands exactly the
// statements denlink issues.
type fakeDB struct {
	mu  sync.Mutex
	row *fakeRow
}

type fakeRow struct {
	baseURL, credential, moduleID, lastError string
	pairedAt                                 time.Time
	syncedAt, revokedAt                      *time.Time
	bundle                                   []byte
}

type rowFunc func(dest ...any) error

func (f rowFunc) Scan(dest ...any) error { return f(dest...) }

func (f *fakeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(sql, "FROM den_link") {
		return rowFunc(func(...any) error { return errors.New("fakeDB: unexpected query " + sql) })
	}
	if f.row == nil {
		return rowFunc(func(...any) error { return pgx.ErrNoRows })
	}
	r := *f.row
	return rowFunc(func(d ...any) error {
		*d[0].(*string), *d[1].(*string), *d[2].(*string) = r.baseURL, r.credential, r.moduleID
		*d[3].(*time.Time), *d[4].(**time.Time), *d[5].(*string) = r.pairedAt, r.syncedAt, r.lastError
		*d[6].(*[]byte), *d[7].(**time.Time) = r.bundle, r.revokedAt
		return nil
	})
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	switch {
	case strings.Contains(sql, "INSERT INTO den_link"):
		f.row = &fakeRow{baseURL: args[0].(string), credential: args[1].(string), moduleID: args[2].(string), pairedAt: now}
	case strings.Contains(sql, "DELETE FROM den_link"):
		f.row = nil
	case f.row == nil:
	case strings.Contains(sql, "SET credential = $1"):
		if f.row.credential == args[1].(string) {
			f.row.credential = args[0].(string)
		}
	case strings.Contains(sql, "SET bundle = $1"):
		f.row.bundle, f.row.syncedAt, f.row.lastError, f.row.revokedAt = args[0].([]byte), &now, "", nil
	case strings.Contains(sql, "revoked_at = COALESCE"):
		if f.row.revokedAt == nil {
			f.row.revokedAt = &now
		}
		f.row.lastError = args[0].(string)
	case strings.Contains(sql, "SET last_error = $1"):
		f.row.lastError = args[0].(string)
	default:
		return pgconn.CommandTag{}, errors.New("fakeDB: unexpected statement " + sql)
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeDB) stored() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row == nil {
		return ""
	}
	return f.row.credential
}

func newFakeClient(t *testing.T, cfg Config, fdb *fakeDB) *Client {
	t.Helper()
	c, err := Open(nil, cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	c.pool = fdb
	return c
}

// fakeDen is a Den that redeems one token and serves the bundle, or answers
// 401 once revoked is set.
type fakeDen struct {
	*httptest.Server
	revoked  atomic.Bool
	cred     atomic.Value
	redeemed atomic.Int32
}

func (d *fakeDen) credential() string { return d.cred.Load().(string) }

func newFakeDen(t *testing.T) *fakeDen {
	d := &fakeDen{}
	d.cred.Store("module-credential-123")
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/integrations/pairing/redeem":
			d.redeemed.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"credential": d.credential(), "module_id": "mod-1"})
		case r.Header.Get("Authorization") != "Bearer "+d.credential() || d.revoked.Load():
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"invalid or revoked credential"}}`))
		case r.URL.Path == "/api/v1/integrations/bundle":
			_, _ = w.Write([]byte(`{"brand":{"revision":3,"organization_name":"Fur Con","assets":{}},"settings":{"data":{"x":true}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.Close)
	return d
}

func TestParseEncryptionKey(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	for _, s := range []string{
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		" " + base64.RawURLEncoding.EncodeToString(raw) + "\n",
	} {
		if b, err := ParseEncryptionKey(s); err != nil || !bytes.Equal(b, raw) {
			t.Errorf("ParseEncryptionKey(%q) = %v", s, err)
		}
	}
	for _, s := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := ParseEncryptionKey(s); err == nil {
			t.Errorf("ParseEncryptionKey(%q): want error", s)
		}
	}
}

func TestKeyringRoundTripAndRotation(t *testing.T) {
	old, _ := newKeyring(testKey(1), "")
	sealed, err := old.seal("secret")
	if err != nil || !strings.HasPrefix(sealed, encPrefix) || strings.Contains(sealed, "secret") {
		t.Fatalf("seal = %q, %v", sealed, err)
	}
	if plain, rewrite, err := old.open(sealed); plain != "secret" || rewrite || err != nil {
		t.Errorf("open = %q %v %v", plain, rewrite, err)
	}
	other, _ := old.seal("secret")
	if other == sealed {
		t.Error("two seals of the same value are identical (nonce reuse)")
	}

	rotated, _ := newKeyring(testKey(2), testKey(1))
	if plain, rewrite, err := rotated.open(sealed); plain != "secret" || !rewrite || err != nil {
		t.Errorf("open with previous key = %q %v %v, want rewrite", plain, rewrite, err)
	}
	if plain, rewrite, err := rotated.open("plain-credential"); plain != "plain-credential" || !rewrite || err != nil {
		t.Errorf("plaintext = %q %v %v, want rewrite", plain, rewrite, err)
	}

	wrong, _ := newKeyring(testKey(3), "")
	if _, _, err := wrong.open(sealed); err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Errorf("wrong key: %v", err)
	}
	var none *keyring
	if _, _, err := none.open(sealed); err == nil || !strings.Contains(err.Error(), EnvEncryptionKey) {
		t.Errorf("no key, encrypted row: %v", err)
	}
	if plain, rewrite, err := none.open("plain"); plain != "plain" || rewrite || err != nil {
		t.Errorf("no key, plaintext row: %q %v %v", plain, rewrite, err)
	}
	for _, bad := range []string{encPrefix + "nocolon", encPrefix + "abcd:!!!", encPrefix + old.current.id + ":AAAA"} {
		if _, _, err := old.open(bad); err == nil {
			t.Errorf("open(%q): want error", bad)
		}
	}
	i := len(sealed) - 10
	swap := byte('A')
	if sealed[i] == 'A' {
		swap = 'B'
	}
	tampered := sealed[:i] + string(swap) + sealed[i+1:]
	if _, _, err := old.open(tampered); err == nil {
		t.Error("tampered ciphertext accepted")
	}
}

func TestConfigValidate(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string // "" = valid
	}{
		{"production needs a key", map[string]string{}, EnvEncryptionKey + " is required"},
		{"explicit production needs a key", map[string]string{"APP_ENV": "production"}, EnvEncryptionKey + " is required"},
		{"development without key", map[string]string{"APP_ENV": "development"}, ""},
		{"production with key", map[string]string{EnvEncryptionKey: testKey(1)}, ""},
		{"short key", map[string]string{EnvEncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 31))}, "32 bytes"},
		{"previous without current", map[string]string{"APP_ENV": "development", EnvEncryptionKeyPrevious: testKey(1)}, "is set but"},
		{"bad previous", map[string]string{EnvEncryptionKey: testKey(1), EnvEncryptionKeyPrevious: "x"}, EnvEncryptionKeyPrevious},
		{"bad mode", map[string]string{EnvEncryptionKey: testKey(1), EnvMode: "maybe"}, "DEN_MODE"},
		{"bad internal url", map[string]string{EnvEncryptionKey: testKey(1), EnvInternalURL: "den-backend:8080"}, EnvInternalURL},
		{"bad allowed host", map[string]string{EnvEncryptionKey: testKey(1), EnvAllowedHosts: "https://den.example.org"}, EnvAllowedHosts},
		{"good allowed hosts", map[string]string{EnvEncryptionKey: testKey(1), EnvAllowedHosts: " den.example.org, *.con.example.org ,"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := FromEnv("hr", "1.0.0", env(tc.env))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				if _, err := Open(nil, cfg, zerolog.Nop()); err != nil {
					t.Fatalf("Open: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want it to contain %q", err, tc.want)
			}
			if _, err := Open(nil, cfg, zerolog.Nop()); err == nil {
				t.Fatal("Open accepted an invalid config")
			}
			if err := New(nil, cfg, zerolog.Nop()).Restore(context.Background()); err == nil {
				t.Fatal("Restore accepted an invalid config")
			}
		})
	}
	cfg, _ := FromEnv("hr", "1.0.0", env(map[string]string{EnvEncryptionKey: testKey(1), EnvAllowedHosts: "Den.Example.org, *.con.example.org"}))
	if strings.Join(cfg.AllowedHosts, "|") != "den.example.org|*.con.example.org" {
		t.Errorf("AllowedHosts = %q", cfg.AllowedHosts)
	}
	cfg, err := FromEnv("hr", "1.0.0", env(map[string]string{"APP_ENV": " Development "}))
	if err != nil || cfg.AppEnv != "development" {
		t.Errorf("APP_ENV=' Development ': AppEnv = %q, err = %v; want development without a key", cfg.AppEnv, err)
	}
	if cfg, _ := FromEnv("hr", "1.0.0", env(nil)); cfg.AppEnv != "production" {
		t.Errorf("unset APP_ENV: AppEnv = %q, want production", cfg.AppEnv)
	}
	if !(Config{AppEnv: "DEVELOPMENT"}).HostAllowed("localhost") {
		t.Error("a hand-built Config must normalise APP_ENV too")
	}
}

// The same cases are in authbase's TestNormalizeAppEnv; keep them identical.
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

func TestHostAllowed(t *testing.T) {
	prod := Config{AllowedHosts: []string{"den.example.org", "*.con.example.org", "10.0.0.5"}}
	dev := Config{AppEnv: "development", AllowedHosts: []string{"den.example.org"}}
	cases := []struct {
		cfg  Config
		host string
		want bool
	}{
		{prod, "den.example.org", true},
		{prod, "DEN.example.org.", true},
		{prod, "evil.example.org", false},
		{prod, "den.example.org.evil.net", false},
		{prod, "den.con.example.org", true},
		{prod, "a.b.con.example.org", true},
		{prod, "con.example.org", false},
		{prod, "evilcon.example.org", false},
		{prod, "10.0.0.5", true},
		{prod, "10.0.0.6", false},
		{prod, "localhost", false},
		{prod, "127.0.0.1", false},
		{prod, "", false},
		{Config{}, "den.example.org", false},
		{dev, "localhost", true},
		{dev, "127.0.0.1", true},
		{dev, "::1", true},
		{dev, "[::1]", true},
		{dev, "den.conservices.test", true},
		{dev, "conservices.test", false},
		{dev, "den.example.org", true},
		{dev, "169.254.169.254", false},
	}
	for _, tc := range cases {
		if got := tc.cfg.HostAllowed(tc.host); got != tc.want {
			t.Errorf("AppEnv=%q Allowed=%q HostAllowed(%q) = %v, want %v", tc.cfg.AppEnv, tc.cfg.AllowedHosts, tc.host, got, tc.want)
		}
	}
}

func TestPairTarget(t *testing.T) {
	prod := New(nil, Config{EncryptionKey: testKey(1), AllowedHosts: []string{"den.example.org"}}, zerolog.Nop())
	internal := New(nil, Config{EncryptionKey: testKey(1), InternalURL: "https://den-backend.conservices-den.svc.cluster.local:8080"}, zerolog.Nop())
	dev := New(nil, Config{AppEnv: "development"}, zerolog.Nop())
	cases := []struct {
		c       *Client
		in      string
		want    string
		wantErr string
	}{
		{prod, "https://den.example.org/api/v1/integrations/pairing/redeem?token=abc", "https://den.example.org/api/v1/integrations/pairing/redeem?token=abc", ""},
		{prod, "  https://den.example.org:8443/api/v1/integrations/pairing/redeem?token=abc ", "https://den.example.org:8443/api/v1/integrations/pairing/redeem?token=abc", ""},
		{prod, "https://evil.example.net/api/v1/integrations/pairing/redeem?token=abc", "", "not allowed"},
		{prod, "http://den.example.org/api/v1/integrations/pairing/redeem?token=abc", "", "https://"},
		{prod, "https://den.example.org/redeem", "", "full pairing URL"},
		{prod, "not a url", "", "full pairing URL"},
		{internal, "https://evil.example.net/api/v1/integrations/pairing/redeem?token=abc", "https://den-backend.conservices-den.svc.cluster.local:8080/api/v1/integrations/pairing/redeem?token=abc", ""},
		{dev, "http://localhost:8090/api/v1/integrations/pairing/redeem?token=abc", "http://localhost:8090/api/v1/integrations/pairing/redeem?token=abc", ""},
		{dev, "https://den.conservices.test/api/v1/integrations/pairing/redeem?token=abc", "https://den.conservices.test/api/v1/integrations/pairing/redeem?token=abc", ""},
		{dev, "http://169.254.169.254/latest/meta-data?token=abc", "", "not allowed"},
		{dev, "ftp://localhost/x?token=abc", "", "https://"},
	}
	for _, tc := range cases {
		u, err := tc.c.pairTarget(tc.in)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("pairTarget(%q) = %v, %v; want error %q", tc.in, u, err, tc.wantErr)
			}
			continue
		}
		if err != nil || u.String() != tc.want {
			t.Errorf("pairTarget(%q) = %v, %v; want %s", tc.in, u, err, tc.want)
		}
	}
}

// Regression for the SSRF finding: with DEN_INTERNAL_URL empty, a pasted URL
// pointing at a host outside DEN_ALLOWED_HOSTS is never contacted.
func TestPairNeverContactsDisallowedHost(t *testing.T) {
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer evil.Close()

	c := New(nil, Config{Slug: "tasks", EncryptionKey: testKey(1), AllowedHosts: []string{"den.example.org"}}, zerolog.Nop())
	_, err := c.Pair(context.Background(), evil.URL+"/api/v1/integrations/pairing/redeem?token=abc")
	if err == nil || hits.Load() != 0 {
		t.Fatalf("err %v, attacker host contacted %d times", err, hits.Load())
	}

	var denHits atomic.Int32
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		denHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer den.Close()
	pinned := New(nil, Config{Slug: "tasks", EncryptionKey: testKey(1), InternalURL: den.URL}, zerolog.Nop())
	_, _ = pinned.Pair(context.Background(), evil.URL+"/api/v1/integrations/pairing/redeem?token=abc")
	if hits.Load() != 0 || denHits.Load() != 1 {
		t.Fatalf("with DEN_INTERNAL_URL: attacker %d, den %d", hits.Load(), denHits.Load())
	}
}

func TestPairDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/internal", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	c := New(nil, Config{AppEnv: "development"}, zerolog.Nop())
	if _, err := c.Pair(context.Background(), redirector.URL+"/redeem?token=abc"); err == nil {
		t.Fatal("Pair succeeded through a redirect")
	}
	if hits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestPairEncryptsRevokesAndPairsAgain(t *testing.T) {
	den := newFakeDen(t)
	fdb := &fakeDB{}
	c := newFakeClient(t, Config{Slug: "hr", Mode: ModeRequired, AppEnv: "development", EncryptionKey: testKey(1)}, fdb)
	ctx := context.Background()

	st, err := c.Pair(ctx, den.URL+"/api/v1/integrations/pairing/redeem?token=abc")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Paired || st.Revoked || st.SyncedAt == nil || st.ModuleID != "mod-1" {
		t.Fatalf("status after pair: %+v", st)
	}
	if s := fdb.stored(); !strings.HasPrefix(s, encPrefix) || strings.Contains(s, den.credential()) {
		t.Fatalf("credential stored as %q, want encrypted", s)
	}
	if v, ok := c.BoolSetting("x"); !v || !ok || c.OrganizationName() != "Fur Con" {
		t.Fatalf("bundle not applied: %v %v %q", v, ok, c.OrganizationName())
	}

	den.revoked.Store(true)
	if err := c.Sync(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Sync after revoke = %v, want ErrRevoked", err)
	}
	if !c.Revoked() || !c.Linked() {
		t.Fatalf("Revoked=%v Linked=%v", c.Revoked(), c.Linked())
	}
	st, _ = c.Status(ctx)
	if !st.Revoked || st.RevokedAt == nil || st.LastError != UserMessage(ErrRevoked) {
		t.Fatalf("status after revoke: %+v", st)
	}
	rec := httptest.NewRecorder()
	c.StateHandler(rec, httptest.NewRequest(http.MethodGet, "/api/den/state", nil))
	if got := strings.TrimSpace(rec.Body.String()); got != `{"mode":"required","paired":true,"revoked":true}` {
		t.Fatalf("state %s", got)
	}
	rec = httptest.NewRecorder()
	c.SyncHandler(rec, httptest.NewRequest(http.MethodPost, "/api/den/sync", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "den_credential_revoked") || !strings.Contains(rec.Body.String(), UserMessage(ErrRevoked)) {
		t.Fatalf("SyncHandler: %d %s", rec.Code, rec.Body.String())
	}
	if c.Bundle() == nil {
		t.Fatal("the last bundle was dropped on revoke")
	}
	if n := den.redeemed.Load(); n != 1 {
		t.Fatalf("redeemed %d times; a revoked link must never re-pair by itself", n)
	}

	// A fresh Client (restart) sees the revoked state from the database.
	restarted := newFakeClient(t, Config{Slug: "hr", Mode: ModeRequired, AppEnv: "development", EncryptionKey: testKey(1)}, fdb)
	if err := restarted.Restore(ctx); err != nil || !restarted.Revoked() {
		t.Fatalf("restore: %v revoked=%v", err, restarted.Revoked())
	}

	den.revoked.Store(false)
	den.cred.Store("module-credential-456")
	if _, err := c.Pair(ctx, den.URL+"/api/v1/integrations/pairing/redeem?token=def"); err != nil {
		t.Fatal(err)
	}
	if c.Revoked() {
		t.Fatal("still revoked after pairing again")
	}
	st, _ = c.Status(ctx)
	if st.Revoked || st.LastError != "" {
		t.Fatalf("status after re-pair: %+v", st)
	}

	if err := c.Unpair(ctx); err != nil || c.Linked() || c.Revoked() || fdb.stored() != "" {
		t.Fatalf("unpair: %v linked=%v revoked=%v", err, c.Linked(), c.Revoked())
	}
}

func TestPlaintextRowIsEncryptedOnRead(t *testing.T) {
	den := newFakeDen(t)
	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: den.credential(), pairedAt: time.Now()}}
	c := newFakeClient(t, Config{Slug: "hr", EncryptionKey: testKey(1)}, fdb)
	if err := c.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := fdb.stored(); !strings.HasPrefix(s, encPrefix) {
		t.Fatalf("plaintext row not re-encrypted: %q", s)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatalf("sync with the re-encrypted credential: %v", err)
	}
}

func TestRotationReencryptsWithCurrentKey(t *testing.T) {
	den := newFakeDen(t)
	oldKeys, _ := newKeyring(testKey(1), "")
	sealedOld, _ := oldKeys.seal(den.credential())
	fdb := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: sealedOld, pairedAt: time.Now()}}

	c := newFakeClient(t, Config{Slug: "hr", EncryptionKey: testKey(2), PreviousEncryptionKey: testKey(1)}, fdb)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatalf("sync during rotation: %v", err)
	}
	s := fdb.stored()
	if s == sealedOld {
		t.Fatal("row still sealed with the previous key")
	}
	newOnly, _ := newKeyring(testKey(2), "")
	if plain, rewrite, err := newOnly.open(s); err != nil || rewrite || plain != den.credential() {
		t.Fatalf("row not readable with the new key alone: %q %v %v", plain, rewrite, err)
	}

	lost := newFakeClient(t, Config{Slug: "hr", EncryptionKey: testKey(3)}, &fakeDB{row: &fakeRow{credential: sealedOld}})
	if err := lost.Restore(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestDevelopmentWithoutKeyStoresPlaintext(t *testing.T) {
	den := newFakeDen(t)
	fdb := &fakeDB{}
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, fdb)
	if _, err := c.Pair(context.Background(), den.URL+"/api/v1/integrations/pairing/redeem?token=abc"); err != nil {
		t.Fatal(err)
	}
	if fdb.stored() != den.credential() {
		t.Fatalf("stored %q", fdb.stored())
	}
}
