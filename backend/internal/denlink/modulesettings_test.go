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

	"github.com/rs/zerolog"
)

const contractBundle = `{
  "brand": {"revision": 10, "organization_name": "Con Services Demo", "assets": {}},
  "settings": {"data": {"organization_name": "Con Services Demo"}, "updated_at": "2026-11-05T13:58:34Z"},
  "module_settings": {
    "updated_at": "2026-11-11T09:30:00Z",
    "data": {
      "retention": {"offboarded_volunteers_days": 365, "inactive_users_days": 730, "module_data_days": 0},
      "smtp": {"host": "smtp.example.org", "username": "conservices", "password_set": true,
               "from": "finance@con.example.org", "from_name": "Finance"},
      "finance": {"seller": {"legal_name": "Fur Con e.V.", "address": "Main Street 1\n1010 Vienna",
                             "country": "AT", "vat_id": "ATU12345678"}},
      "wiki": {"revision_retention_days": 90, "share_links": true, "odd": 2.5, "neg": -1}
    }
  }
}`

func mustBundle(t *testing.T, raw string) *Bundle {
	t.Helper()
	var b Bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("bundle does not parse: %v", err)
	}
	return &b
}

func TestModuleSettingsContract(t *testing.T) {
	ms := mustBundle(t, contractBundle).ModuleSettings
	if ms == nil || ms.UpdatedAt == nil || *ms.UpdatedAt != "2026-11-11T09:30:00Z" {
		t.Fatalf("module settings %+v", ms)
	}

	r := ms.Retention()
	if r.OffboardedVolunteersDays == nil || *r.OffboardedVolunteersDays != 365 ||
		r.InactiveUsersDays == nil || *r.InactiveUsersDays != 730 ||
		r.ModuleDataDays == nil || *r.ModuleDataDays != 0 {
		t.Errorf("retention %+v", r)
	}

	s, ok := ms.SMTP()
	if !ok || s.Host != "smtp.example.org" || s.Port != 587 || s.Security != "starttls" || !s.PasswordSet || s.FromName != "Finance" {
		t.Errorf("smtp %+v %v", s, ok)
	}

	seller, ok := ms.FinanceSeller()
	if !ok || seller.LegalName != "Fur Con e.V." || seller.VATID != "ATU12345678" || !strings.Contains(seller.Address, "\n") || seller.Country != "AT" {
		t.Errorf("seller %+v %v", seller, ok)
	}

	if d, ok := ms.Days("wiki.revision_retention_days"); !ok || d != 90 {
		t.Errorf("wiki days %d %v", d, ok)
	}
	if b, ok := ms.Bool("wiki.share_links"); !b || !ok {
		t.Errorf("wiki bool %v %v", b, ok)
	}
	for _, k := range []string{"wiki.odd", "wiki.neg", "wiki.missing", "missing.key", "wiki.share_links"} {
		if _, ok := ms.Days(k); ok {
			t.Errorf("Days(%q) ok, want not set", k)
		}
	}
	if n, ok := ms.Int("wiki.neg"); !ok || n != -1 {
		t.Errorf("Int(wiki.neg) = %d %v", n, ok)
	}
	if _, ok := ms.String("wiki.revision_retention_days"); ok {
		t.Error("a number read as a string")
	}
	if raw, ok := ms.Section("wiki"); !ok || !strings.Contains(string(raw), "revision_retention_days") {
		t.Errorf("Section(wiki) = %s %v", raw, ok)
	}
	if _, ok := ms.Section("wiki.share_links"); ok {
		t.Error("Section accepted a dotted key")
	}
	var wiki struct {
		Days int `json:"revision_retention_days"`
	}
	if ok, err := ms.Decode("wiki", &wiki); !ok || err != nil || wiki.Days != 90 {
		t.Errorf("Decode(wiki) %v %v %+v", ok, err, wiki)
	}
	var wrong []string
	if ok, err := ms.Decode("wiki", &wrong); !ok || err == nil {
		t.Errorf("Decode into the wrong type: %v %v", ok, err)
	}
}

func TestModuleSettingsNilAndAbsent(t *testing.T) {
	var ms *ModuleSettings
	if _, ok := ms.Lookup("retention"); ok {
		t.Error("nil Lookup ok")
	}
	if _, ok := ms.SMTP(); ok {
		t.Error("nil SMTP ok")
	}
	if _, ok := ms.FinanceSeller(); ok {
		t.Error("nil FinanceSeller ok")
	}
	if r := ms.Retention(); r != (Retention{}) {
		t.Errorf("nil Retention %+v", r)
	}
	if _, ok := ms.Days("wiki.revision_retention_days"); ok {
		t.Error("nil Days ok")
	}

	// A bundle stored before Den had module settings.
	old := mustBundle(t, `{"brand":{"revision":3,"organization_name":"Fur Con"},"settings":{"data":{"x":true}}}`)
	if old.ModuleSettings != nil || old.Brand.OrganizationName != "Fur Con" {
		t.Fatalf("old bundle: %+v", old)
	}

	empty := mustBundle(t, `{"brand":{},"settings":{"data":{}},"module_settings":{"data":{"smtp":{"host":" "},"retention":null,"finance":{"seller":null}}}}`)
	if _, ok := empty.ModuleSettings.SMTP(); ok {
		t.Error("smtp without a host counts as set")
	}
	if _, ok := empty.ModuleSettings.Section("retention"); ok {
		t.Error("null section counts as set")
	}
	if _, ok := empty.ModuleSettings.FinanceSeller(); ok {
		t.Error("null seller counts as set")
	}
}

func TestModuleSettingsLegacyAndMalformed(t *testing.T) {
	// Den before the contract sent the sections at the top level.
	legacy := mustBundle(t, `{"brand":{"organization_name":"Fur Con"},"settings":{"data":{}},
		"module_settings":{"updated_at":"2026-11-01T00:00:00Z","retention":{"inactive_users_days":30},
		"smtp":{"host":"mail.example.org","port":465,"security":"tls","password":"never-read"}}}`)
	ms := legacy.ModuleSettings
	if ms == nil || ms.UpdatedAt == nil || *ms.UpdatedAt != "2026-11-01T00:00:00Z" {
		t.Fatalf("legacy %+v", ms)
	}
	if r := ms.Retention(); r.InactiveUsersDays == nil || *r.InactiveUsersDays != 30 {
		t.Errorf("legacy retention %+v", r)
	}
	if s, ok := ms.SMTP(); !ok || s.Port != 465 || s.Security != "tls" {
		t.Errorf("legacy smtp %+v", s)
	}
	if _, ok := ms.Lookup("updated_at"); ok {
		t.Error("updated_at read as a section")
	}

	// A malformed module_settings must not cost the module its brand.
	for _, bad := range []string{`"text"`, `[1,2]`, `{"data":"x"}`, `{"data":{"smtp":"x"}}`, `null`} {
		b := mustBundle(t, `{"brand":{"organization_name":"Fur Con"},"settings":{"data":{}},"module_settings":`+bad+`}`)
		if b.Brand.OrganizationName != "Fur Con" {
			t.Errorf("%s: brand lost", bad)
		}
		if _, ok := b.ModuleSettings.SMTP(); ok {
			t.Errorf("%s: smtp set", bad)
		}
	}
}

// Den's module settings are stored with the bundle and come back after a
// restart without contacting Den.
func TestModuleSettingsSurviveRestart(t *testing.T) {
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/integrations/pairing/redeem":
			_ = json.NewEncoder(w).Encode(map[string]string{"credential": "cred", "module_id": "mod-1"})
		case "/api/v1/integrations/bundle":
			_, _ = w.Write([]byte(contractBundle))
		default:
			http.NotFound(w, r)
		}
	}))
	defer den.Close()
	cfg := Config{Slug: "wiki", AppEnv: "development", EncryptionKey: testKey(1)}
	fdb := &fakeDB{}
	c := newFakeClient(t, cfg, fdb)
	if _, err := c.Pair(context.Background(), den.URL+"/api/v1/integrations/pairing/redeem?token=abc"); err != nil {
		t.Fatal(err)
	}
	if d, ok := c.ModuleSettings().Days("wiki.revision_retention_days"); !ok || d != 90 {
		t.Fatalf("after pair: %d %v", d, ok)
	}
	den.Close()

	restarted := newFakeClient(t, cfg, fdb)
	if restarted.ModuleSettings() != nil {
		t.Fatal("module settings before Restore")
	}
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d, ok := restarted.ModuleSettings().Days("wiki.revision_retention_days"); !ok || d != 90 {
		t.Fatalf("after restart: %d %v", d, ok)
	}
	if s, ok := restarted.ModuleSettings().FinanceSeller(); !ok || s.LegalName != "Fur Con e.V." {
		t.Fatalf("seller after restart: %+v", s)
	}

	// A row stored by an older denlink, without module_settings.
	oldRow := &fakeDB{row: &fakeRow{baseURL: den.URL, credential: "cred", pairedAt: time.Now(),
		bundle: []byte(`{"brand":{"organization_name":"Fur Con"},"settings":{"data":{}}}`)}}
	old := newFakeClient(t, cfg, oldRow)
	if err := old.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !old.Paired() || old.ModuleSettings() != nil || old.OrganizationName() != "Fur Con" {
		t.Fatalf("old row: paired=%v ms=%v org=%q", old.Paired(), old.ModuleSettings(), old.OrganizationName())
	}
	if _, ok := old.ModuleSettings().Days("wiki.revision_retention_days"); ok {
		t.Fatal("old row has module settings")
	}
}

func TestHandlerBodies(t *testing.T) {
	c := New(nil, Config{AppEnv: "development"}, zerolog.Nop())
	cases := []struct {
		name, body, code string
		status           int
	}{
		{"too large", `{"pairing_url":"` + strings.Repeat("a", maxRequestBody) + `"}`, "request_too_large", http.StatusRequestEntityTooLarge},
		{"not json", `pairing`, "bad_request", http.StatusBadRequest},
		{"two values", `{"pairing_url":"x"} {"pairing_url":"y"}`, "bad_request", http.StatusBadRequest},
		{"empty", ``, "bad_request", http.StatusBadRequest},
		{"valid but unusable", `{"pairing_url":"nope"}` + "\n", "pairing_failed", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.PairHandler(rec, httptest.NewRequest(http.MethodPost, "/api/den/pair", strings.NewReader(tc.body)))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Errorf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}

	prefs := c.PreferencesHandler(func(*http.Request) (string, string, bool) { return "", "", false })
	rec := httptest.NewRecorder()
	prefs(rec, httptest.NewRequest(http.MethodPut, "/api/me/preferences", strings.NewReader(`{"theme":"`+strings.Repeat("d", maxRequestBody)+`"}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("preferences too large: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	prefs(rec, httptest.NewRequest(http.MethodPut, "/api/me/preferences", strings.NewReader(`{"theme":"dark"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"theme":"dark"`) {
		t.Errorf("preferences: %d %s", rec.Code, rec.Body.String())
	}
}
