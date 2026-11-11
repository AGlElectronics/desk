package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
)

// TestDenPairingGate checks the routing of DEN_MODE=required on the real
// router: the state endpoint is public, the Den admin routes still go
// through authentication, and other API routes answer 503. No database is
// needed: OIDC without a verifier refuses every request before a handler runs.
func TestDenPairingGate(t *testing.T) {
	cfg := &config.Config{AuthMode: config.AuthModeOIDC, Den: denlink.Config{Mode: denlink.ModeRequired}}
	den := denlink.New(nil, cfg.Den, zerolog.Nop())
	router := NewRouter(Deps{Config: cfg, Logger: zerolog.Nop(), Den: den}, "test")
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for _, path := range []string{"/api/den/state", "/api/v1/den/state"} {
		rec := get(path)
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || got != `{"mode":"required","paired":false,"revoked":false}` {
			t.Errorf("GET %s = %d %s", path, rec.Code, got)
		}
	}

	rec := get("/api/v1/conversations")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Error.Code != "den_pairing_required" {
		t.Errorf("GET /api/v1/conversations = %d %s, want 503 den_pairing_required", rec.Code, rec.Body.String())
	}

	rec = get("/api/v1/den/")
	if rec.Code == http.StatusOK || rec.Code == http.StatusNotFound || rec.Code == http.StatusServiceUnavailable {
		t.Errorf("GET /api/v1/den/ = %d %s, want it refused by authentication", rec.Code, rec.Body.String())
	}
}
