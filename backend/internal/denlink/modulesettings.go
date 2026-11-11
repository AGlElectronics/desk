// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// ModuleSettings is the module_settings part of Den's bundle: the settings
// an organisation decides once in Den (retention, mail, seller details, …),
// already narrowed by Den to the calling module. Contract:
// docs/concepts/module-settings.md.
//
// Data holds the top-level sections as raw JSON: the shared sections
// "retention" and "smtp", and the module's own section under its slug
// ("wiki", "finance", "stash", …). A key Den leaves out is absent; the
// module then uses its own setting.
//
// The methods are safe on a nil *ModuleSettings (standalone, or a Den that
// sends none): they report "not set".
type ModuleSettings struct {
	Data      map[string]json.RawMessage `json:"data"`
	UpdatedAt *string                    `json:"updated_at,omitempty"`
}

// UnmarshalJSON reads the contract shape {"data": {…}, "updated_at": "…"}.
// Bundles stored before the contract carry the sections at the top level
// ({"retention": {…}, "finance": {…}, "updated_at": "…"}); they are read as
// if wrapped in "data". It never fails: a malformed value leaves the
// settings empty, so one bad section cannot discard the whole bundle.
func (m *ModuleSettings) UnmarshalJSON(raw []byte) error {
	*m = ModuleSettings{}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || top == nil {
		return nil
	}
	if u, ok := top["updated_at"]; ok {
		var s string
		if json.Unmarshal(u, &s) == nil {
			m.UpdatedAt = &s
		}
		delete(top, "updated_at")
	}
	if d, ok := top["data"]; ok {
		var data map[string]json.RawMessage
		if json.Unmarshal(d, &data) == nil {
			m.Data = data
		}
		return nil
	}
	if len(top) > 0 {
		m.Data = top
	}
	return nil
}

// Lookup returns the raw value at a dotted key such as
// "wiki.revision_retention_days" or "finance.seller". ok is false when the
// key is absent or null.
func (m *ModuleSettings) Lookup(key string) (json.RawMessage, bool) {
	if m == nil || m.Data == nil {
		return nil, false
	}
	parts := strings.Split(key, ".")
	cur, ok := m.Data[parts[0]]
	for _, p := range parts[1:] {
		if !ok {
			break
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(cur, &obj) != nil {
			return nil, false
		}
		cur, ok = obj[p]
	}
	if !ok || isNull(cur) {
		return nil, false
	}
	return cur, true
}

// Section is the raw value of one top-level section, for example
// Section("wiki") for the Wiki module's own settings.
func (m *ModuleSettings) Section(name string) (json.RawMessage, bool) {
	if strings.Contains(name, ".") {
		return nil, false
	}
	return m.Lookup(name)
}

// Decode unmarshals the value at a dotted key into dst. ok is false when the
// key is absent; err is set when it is present but does not fit dst.
func (m *ModuleSettings) Decode(key string, dst any) (ok bool, err error) {
	raw, ok := m.Lookup(key)
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dst)
}

// String returns a string setting. ok is false when absent or not a string.
func (m *ModuleSettings) String(key string) (string, bool) {
	var s string
	if ok, err := m.Decode(key, &s); !ok || err != nil {
		return "", false
	}
	return s, true
}

// Bool returns a boolean setting. ok is false when absent or not a boolean.
func (m *ModuleSettings) Bool(key string) (value, ok bool) {
	var b bool
	if ok, err := m.Decode(key, &b); !ok || err != nil {
		return false, false
	}
	return b, true
}

// Int returns a whole-number setting (30 and 30.0 both count). ok is false
// when absent, not a number, or not a whole number.
func (m *ModuleSettings) Int(key string) (int, bool) {
	var f float64
	if ok, err := m.Decode(key, &f); !ok || err != nil {
		return 0, false
	}
	if f != math.Trunc(f) || f > math.MaxInt32 || f < math.MinInt32 {
		return 0, false
	}
	return int(f), true
}

// Days returns a retention-style setting: a whole number of days, 0 or
// more. ok is false otherwise, so a bad value from Den falls back to the
// module's own setting.
func (m *ModuleSettings) Days(key string) (int, bool) {
	d, ok := m.Int(key)
	if !ok || d < 0 {
		return 0, false
	}
	return d, true
}

// Retention is the "retention" section. A nil field is not set by Den.
type Retention struct {
	OffboardedVolunteersDays *int `json:"offboarded_volunteers_days,omitempty"`
	InactiveUsersDays        *int `json:"inactive_users_days,omitempty"`
	ModuleDataDays           *int `json:"module_data_days,omitempty"`
}

// Retention returns the retention section; only valid day counts (whole,
// 0 or more) are filled in.
func (m *ModuleSettings) Retention() Retention {
	get := func(k string) *int {
		if d, ok := m.Days("retention." + k); ok {
			return &d
		}
		return nil
	}
	return Retention{
		OffboardedVolunteersDays: get("offboarded_volunteers_days"),
		InactiveUsersDays:        get("inactive_users_days"),
		ModuleDataDays:           get("module_data_days"),
	}
}

// SMTP is the "smtp" section. The password never travels in the bundle:
// PasswordSet says Den holds one, and the module fetches it from
// GET /api/v1/integrations/module-settings/secrets when it sends mail.
type SMTP struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
}

// SMTP returns Den's mail server. ok is false unless Den sets smtp.host;
// then the module keeps its own SMTP_* settings. Port defaults to 587 and
// Security to "starttls".
func (m *ModuleSettings) SMTP() (SMTP, bool) {
	var s SMTP
	if ok, err := m.Decode("smtp", &s); !ok || err != nil || strings.TrimSpace(s.Host) == "" {
		return SMTP{}, false
	}
	if s.Port == 0 {
		s.Port = 587
	}
	if s.Security == "" {
		s.Security = "starttls"
	}
	return s, true
}

// FinanceSeller is the "finance.seller" section, printed on Finance's
// receipts and invoices.
type FinanceSeller struct {
	LegalName    string `json:"legal_name"`
	Address      string `json:"address"`
	Country      string `json:"country"`
	VATID        string `json:"vat_id"`
	TaxNumber    string `json:"tax_number"`
	Registration string `json:"registration"`
	Email        string `json:"email"`
	Website      string `json:"website"`
}

// FinanceSeller returns finance.seller. ok is false when Den does not set
// it; a field Den leaves empty is "".
func (m *ModuleSettings) FinanceSeller() (FinanceSeller, bool) {
	var s FinanceSeller
	if ok, err := m.Decode("finance.seller", &s); !ok || err != nil {
		return FinanceSeller{}, false
	}
	return s, true
}

func isNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null"
}
