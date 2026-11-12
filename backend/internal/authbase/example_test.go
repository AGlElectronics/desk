// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/AGlElectronics/desk/backend/internal/authbase"
	"github.com/AGlElectronics/desk/backend/internal/groupmatch"
)

// A backend's start-up and API middleware: refuse a bad AUTH_MODE, bound
// request bodies, rate-limit by person and decide access with groupmatch.
func Example() {
	auth, err := authbase.FromEnv(func(k string) string {
		return map[string]string{"APP_ENV": "production"}[k]
	})
	if err != nil {
		fmt.Println("refusing to start:", err)
		os.Exit(1)
	}
	groups := groupmatch.Config{Prefix: "/con2026"}
	limiter := authbase.NewRateLimiter(authbase.RateLimitOptions{Rate: 5, Burst: 10})

	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In a real backend the claims come from the verified ID token.
		claims := map[string]any{"groups": []any{"/con2026/departments/security"}, "roles": []any{"hr-admin"}}
		if !groups.MatchAny(groups.Groups(claims), []string{"departments/security"}) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := authbase.DecodeJSON(w, r, &in, authbase.DecodeOptions{DisallowUnknownFields: true}); err != nil {
			authbase.WriteDecodeError(w, err)
			return
		}
		_, _ = fmt.Fprintf(w, "hello %s", in.Name)
	})
	h := authbase.SecurityHeaders(auth.StripDevHeaders(limiter.Middleware(api)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/things", strings.NewReader(`{"name":"fox"}`)))
	fmt.Println(rec.Code, rec.Body.String(), rec.Header().Get("X-Content-Type-Options"), auth.AuthMode)
	// Output: 200 hello fox nosniff oidc
}
