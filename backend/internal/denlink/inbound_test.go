// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pairedClient(t *testing.T, credential string, revoked bool) *Client {
	t.Helper()
	var revokedAt *time.Time
	if revoked {
		now := time.Now()
		revokedAt = &now
	}
	return newFakeClient(t, Config{Slug: "tasks", AppEnv: "development"}, &fakeDB{row: &fakeRow{
		baseURL: "http://den.example", credential: credential, pairedAt: time.Now(), revokedAt: revokedAt,
	}})
}

func TestCredentialMatches(t *testing.T) {
	const cred = "module-credential-123"
	c := pairedClient(t, cred, false)
	ctx := context.Background()
	if !c.CredentialMatches(ctx, cred) || !c.CredentialMatches(ctx, "  "+cred+"\n") {
		t.Fatal("stored credential should match, including surrounding space")
	}
	for _, bad := range []string{"", "module-credential-124", "module-credential-1234", "module-credential-12"} {
		if c.CredentialMatches(ctx, bad) {
			t.Errorf("matched %q", bad)
		}
	}
	// The value in the database is the credential, not a prefix of it, and
	// a different length must not match either.
	if c.CredentialMatches(ctx, strings.Repeat("x", len(cred))) {
		t.Fatal("same-length wrong token matched")
	}

	keys, err := newKeyring(testKey(1), "")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keys.seal(cred)
	if err != nil {
		t.Fatal(err)
	}
	enc := newFakeClient(t, Config{Slug: "tasks", EncryptionKey: testKey(1)}, &fakeDB{row: &fakeRow{
		baseURL: "http://den.example", credential: sealed, pairedAt: time.Now(),
	}})
	if !enc.CredentialMatches(ctx, cred) || enc.CredentialMatches(ctx, sealed) {
		t.Fatal("comparison must use the decrypted credential")
	}

	if pairedClient(t, cred, true).CredentialMatches(ctx, cred) {
		t.Fatal("revoked credential matched")
	}
	if newFakeClient(t, Config{AppEnv: "development"}, &fakeDB{}).CredentialMatches(ctx, cred) {
		t.Fatal("standalone matched")
	}
	var nilClient *Client
	if nilClient.CredentialMatches(ctx, cred) {
		t.Fatal("nil client matched")
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer module-cred":       "module-cred",
		"bearer module-cred":       "module-cred",
		"  BEARER   module-cred  ": "module-cred",
		"":                         "",
		"Bearer":                   "",
		"Bearer ":                  "",
		"Basic module-cred":        "",
		"Token module-cred":        "",
	}
	for in, want := range cases {
		if got := BearerToken(in); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUserFromDen(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/portal/todos?issuer=query-iss&subject=query-sub", nil)
	if _, _, ok := UserFromDen(req); ok {
		t.Fatal("query parameters counted as the person")
	}
	req.Header.Set(HeaderUserIssuer, "  https://auth.example/realms/conservices  ")
	req.Header.Set(HeaderUserSubject, " bob ")
	iss, sub, ok := UserFromDen(req)
	if !ok || iss != "https://auth.example/realms/conservices" || sub != "bob" {
		t.Fatalf("%q %q %v", iss, sub, ok)
	}
	req.Header.Set(HeaderUserSubject, "")
	if _, _, ok := UserFromDen(req); ok {
		t.Fatal("blank subject accepted")
	}
	req.Header.Set(HeaderUserSubject, strings.Repeat("s", maxDenUserLen))
	if _, _, ok := UserFromDen(req); !ok {
		t.Fatal("512-byte subject rejected")
	}
	req.Header.Set(HeaderUserSubject, strings.Repeat("s", maxDenUserLen+1))
	if _, _, ok := UserFromDen(req); ok {
		t.Fatal("513-byte subject accepted")
	}
	if _, _, ok := UserFromDen(nil); ok {
		t.Fatal("nil request accepted")
	}
}

func TestRequireDen(t *testing.T) {
	const cred = "module-credential-123"
	c := pairedClient(t, cred, false)
	var saw PortalCaller
	var called bool
	h := c.RequireDen(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var ok bool
		saw, ok = PortalCallerFrom(r.Context())
		if !ok {
			t.Fatal("caller missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	call := func(auth, issuer, subject string) *httptest.ResponseRecorder {
		t.Helper()
		called = false
		saw = PortalCaller{}
		req := httptest.NewRequest(http.MethodGet, "/api/portal/todos?issuer=from-query&subject=from-query", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if issuer != "" {
			req.Header.Set(HeaderUserIssuer, issuer)
		}
		if subject != "" {
			req.Header.Set(HeaderUserSubject, subject)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	errBody := func(rec *httptest.ResponseRecorder) (code, msg string) {
		t.Helper()
		var body struct {
			Error struct {
				Code, Message string
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Error.Code, body.Error.Message
	}

	rec := call("Bearer "+cred, "https://auth.example/realms/conservices", "alice")
	if rec.Code != http.StatusNoContent || !called || saw.Issuer == "" || saw.Subject != "alice" {
		t.Fatalf("accepted call: %d called %v %+v", rec.Code, called, saw)
	}
	if _, ok := PortalCallerFrom(context.Background()); ok {
		t.Fatal("caller leaked outside the request")
	}

	rec = call("bearer "+cred, "https://auth.example/realms/conservices", "alice")
	if rec.Code != http.StatusNoContent || saw.Subject != "alice" {
		t.Fatalf("lowercase scheme: %d %+v", rec.Code, saw)
	}

	for _, auth := range []string{"", "Bearer wrong", "Basic " + cred} {
		rec = call(auth, "iss", "sub")
		code, msg := errBody(rec)
		if rec.Code != http.StatusUnauthorized || called || code != "unauthorized" || msg != "invalid or revoked credential" {
			t.Fatalf("auth %q: %d called %v %s %s", auth, rec.Code, called, code, msg)
		}
	}
	rec = call("Bearer "+cred, "", "")
	code, msg := errBody(rec)
	if rec.Code != http.StatusBadRequest || called || code != "bad_request" || msg != "issuer and subject are required" {
		t.Fatalf("no person: %d called %v %s %s", rec.Code, called, code, msg)
	}
	// Query parameters do not fill in the headers Den did not send.
	rec = call("Bearer "+cred, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("query person: %d %s", rec.Code, rec.Body.String())
	}

	revoked := pairedClient(t, cred, true).RequireDen(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("revoked credential reached the handler")
	}))
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/portal/todos", nil)
	req.Header.Set("Authorization", "Bearer "+cred)
	req.Header.Set(HeaderUserIssuer, "iss")
	req.Header.Set(HeaderUserSubject, "sub")
	revoked.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d %s", rec.Code, rec.Body.String())
	}

	alone := newFakeClient(t, Config{AppEnv: "development"}, &fakeDB{}).RequireDen(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("standalone reached the handler")
	}))
	rec = httptest.NewRecorder()
	alone.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("standalone: %d", rec.Code)
	}
}
