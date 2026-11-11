// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestClient(mode Mode, linked bool) *Client {
	c := New(nil, Config{Slug: "test", Mode: mode}, zerolog.Nop())
	c.setLinked(linked, false)
	return c
}

func serve(c *Client, path string) *httptest.ResponseRecorder {
	h := c.RequirePaired(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestModeValidate(t *testing.T) {
	for _, m := range []Mode{"", ModeStandalone, ModeRequired} {
		if err := m.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", m, err)
		}
	}
	for _, m := range []Mode{"Required", "paired", "off"} {
		err := m.Validate()
		if err == nil || !strings.Contains(err.Error(), "DEN_MODE") {
			t.Errorf("Validate(%q) = %v, want a DEN_MODE error", m, err)
		}
	}
}

func TestRequirePairedStandalonePasses(t *testing.T) {
	for _, mode := range []Mode{"", ModeStandalone} {
		if rec := serve(newTestClient(mode, false), "/api/items"); rec.Code != http.StatusOK {
			t.Errorf("mode %q: status %d, want 200", mode, rec.Code)
		}
	}
}

func TestRequirePairedBlocksUnpaired(t *testing.T) {
	c := newTestClient(ModeRequired, false)
	for _, path := range []string{"/api/items", "/api/v1/tickets", "/api/settings", "/api/denied", "/api/v1/brandx", "/api/public/v1/calls"} {
		rec := serve(c, path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body %q: %v", path, rec.Body.String(), err)
		}
		if body.Error.Code != "den_pairing_required" || body.Error.Message != "This module is waiting to be connected to Den." {
			t.Errorf("%s: error %+v", path, body.Error)
		}
	}
}

func TestRequirePairedAllowlist(t *testing.T) {
	c := newTestClient(ModeRequired, false)
	for _, path := range []string{
		"/api/health",
		"/api/role-catalog",
		"/api/brand",
		"/api/brand/logo",
		"/api/me",
		"/api/me/preferences",
		"/api/den",
		"/api/den/",
		"/api/den/state",
		"/api/den/pair",
		"/api/v1/brand",
		"/api/v1/brand/login-background",
		"/api/v1/me",
		"/api/v1/me/preferences",
		"/api/v1/den/",
		"/api/v1/den/state",
		"/api/public/v1/brand",
	} {
		if rec := serve(c, path); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rec.Code)
		}
	}
}

func TestRequirePairedPairedPasses(t *testing.T) {
	if rec := serve(newTestClient(ModeRequired, true), "/api/items"); rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Code)
	}
}

func TestRequirePairedRevokedStillPasses(t *testing.T) {
	c := newTestClient(ModeRequired, true)
	c.setLinked(true, true)
	if rec := serve(c, "/api/items"); rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Code)
	}
}

func TestStateHandler(t *testing.T) {
	cases := []struct {
		mode            Mode
		linked, revoked bool
		want            string
	}{
		{ModeRequired, false, false, `{"mode":"required","paired":false,"revoked":false}`},
		{ModeRequired, true, false, `{"mode":"required","paired":true,"revoked":false}`},
		{ModeStandalone, false, false, `{"mode":"standalone","paired":false,"revoked":false}`},
		{"", true, false, `{"mode":"standalone","paired":true,"revoked":false}`},
		{ModeRequired, true, true, `{"mode":"required","paired":true,"revoked":true}`},
		{ModeRequired, false, true, `{"mode":"required","paired":false,"revoked":false}`},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c := newTestClient(tc.mode, tc.linked)
		c.setLinked(tc.linked, tc.revoked)
		c.StateHandler(rec, httptest.NewRequest(http.MethodGet, "/api/den/state", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%+v: status %d", tc, rec.Code)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != tc.want {
			t.Errorf("%+v: body %s, want %s", tc, got, tc.want)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control %q", cc)
		}
	}
}
