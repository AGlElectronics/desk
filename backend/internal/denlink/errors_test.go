// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestRevokedErrorText(t *testing.T) {
	const want = "Den rejected this module's credential (revoked, or the module was removed in Den); pair again from Den → Modules → Pair"
	if UserMessage(ErrRevoked) != want {
		t.Fatalf("UserMessage = %q", UserMessage(ErrRevoked))
	}
	got := ErrRevoked.Error()
	r, _ := utf8.DecodeRuneInString(got)
	if unicode.IsUpper(r) || strings.HasSuffix(got, ".") || strings.HasSuffix(got, ":") || strings.HasSuffix(got, "!") {
		t.Fatalf("Error() = %q, want a Go error string", got)
	}
	if got != "den rejected this module's credential (revoked, or the module was removed in Den); pair again from Den → Modules → Pair" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(ErrRevoked, ErrRevoked) || errors.Is(ErrRevoked, ErrNotPaired) {
		t.Fatal("errors.Is(ErrRevoked)")
	}
	if UserMessage(nil) != "" || UserMessage(ErrNotPaired) != ErrNotPaired.Error() {
		t.Fatalf("UserMessage fallback: %q %q", UserMessage(nil), UserMessage(ErrNotPaired))
	}
}

func TestDenFailureKeepsTheSentence(t *testing.T) {
	den := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/integrations/pairing/redeem":
			if r.URL.Query().Get("token") == "bad" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"message":"token already used"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"credential":"cred","module_id":"mod-1"}`))
		default:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"bundle unavailable"}}`))
		}
	}))
	defer den.Close()
	c := newFakeClient(t, Config{Slug: "hr", AppEnv: "development"}, &fakeDB{})
	ctx := context.Background()

	_, err := c.Pair(ctx, den.URL+"/api/v1/integrations/pairing/redeem?token=bad")
	if err == nil {
		t.Fatal("bad token paired")
	}
	if got := err.Error(); got != "den refused the pairing URL: token already used" {
		t.Fatalf("error %q", got)
	}
	if got := UserMessage(err); got != "Den refused the pairing URL: token already used" {
		t.Fatalf("message %q", got)
	}
	rec := httptest.NewRecorder()
	c.PairHandler(rec, httptest.NewRequest(http.MethodPost, "/api/den/pair", strings.NewReader(`{"pairing_url":"`+den.URL+`/api/v1/integrations/pairing/redeem?token=bad"}`)))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"message":"Den refused the pairing URL: token already used"`) {
		t.Fatalf("pair handler: %d %s", rec.Code, rec.Body.String())
	}

	st, err := c.Pair(ctx, den.URL+"/api/v1/integrations/pairing/redeem?token=ok")
	if err != nil {
		t.Fatal(err)
	}
	if st.LastError != "Den returned bundle unavailable" {
		t.Fatalf("last_error %q", st.LastError)
	}
	err = c.Sync(ctx)
	if err == nil || err.Error() != "den returned bundle unavailable" || UserMessage(err) != "Den returned bundle unavailable" {
		t.Fatalf("sync %v", err)
	}
	rec = httptest.NewRecorder()
	c.SyncHandler(rec, httptest.NewRequest(http.MethodPost, "/api/den/sync", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"message":"Den returned bundle unavailable"`) {
		t.Fatalf("sync handler: %d %s", rec.Code, rec.Body.String())
	}
}
