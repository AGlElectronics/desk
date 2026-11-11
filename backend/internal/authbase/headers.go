// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import "net/http"

// APISecurityHeaders are set on every API response by SecurityHeaders. An
// API answers JSON (or a copied brand asset), never a page to render, so the
// policy is the strictest one. HSTS is set at the Gateway or Caddy, not here;
// the web apps' headers are in shared/nginx/security-headers.conf.
var APISecurityHeaders = map[string]string{
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "no-referrer",
	"X-Frame-Options":         "DENY",
	"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
}

// SecurityHeaders sets APISecurityHeaders on every response before the
// handler runs; a handler may still replace one of them.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for k, v := range APISecurityHeaders {
			h.Set(k, v)
		}
		next.ServeHTTP(w, r)
	})
}
