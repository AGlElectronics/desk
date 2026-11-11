// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"net/http"
	"strconv"
	"strings"
)

// Dev-mode identity headers. They are honoured, and allowed through CORS,
// only when Config.DevAuth is true.
const (
	HeaderDevUser   = "X-Dev-User"
	HeaderDevGroups = "X-Dev-Groups"
)

// BaseAllowedHeaders are the request headers browsers may send cross-origin
// in every mode.
var BaseAllowedHeaders = []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"}

// AllowedHeaders is BaseAllowedHeaders plus extra, plus X-Dev-User and
// X-Dev-Groups when c.DevAuth. Pass it to go-chi/cors (AllowedHeaders) or
// use CORS.
func (c Config) AllowedHeaders(extra ...string) []string {
	out := append(append([]string{}, BaseAllowedHeaders...), extra...)
	if c.DevAuth() {
		out = append(out, HeaderDevUser, HeaderDevGroups)
	}
	return out
}

// StripDevHeaders removes X-Dev-User and X-Dev-Groups from every request
// unless c.DevAuth, so no later handler can trust them by mistake.
func (c Config) StripDevHeaders(next http.Handler) http.Handler {
	if c.DevAuth() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(HeaderDevUser)
		r.Header.Del(HeaderDevGroups)
		next.ServeHTTP(w, r)
	})
}

// CORSOptions configures CORS.
type CORSOptions struct {
	// AllowedOrigins are exact origins such as https://hr.example.org
	// (CORS_ALLOWED_ORIGINS). "*" is not supported: requests carry
	// credentials.
	AllowedOrigins []string
	// AllowedMethods default to GET, POST, PUT, PATCH, DELETE.
	AllowedMethods []string
	// ExtraHeaders are allowed in addition to Config.AllowedHeaders.
	ExtraHeaders []string
	// MaxAge is how long browsers cache a preflight, in seconds (default 300).
	MaxAge int
	// ExposeHeaders are the response headers the browser lets the page
	// read (Access-Control-Expose-Headers). nil means DefaultExposeHeaders;
	// an empty, non-nil slice exposes none.
	ExposeHeaders []string
}

// DefaultExposeHeaders are exposed when CORSOptions.ExposeHeaders is nil:
// X-Request-ID, so the web app can show it in error reports.
var DefaultExposeHeaders = []string{"X-Request-ID"}

// CORS answers preflight requests and sets Access-Control-* headers for
// the allowed origins. Requests from other origins get no CORS headers (the
// browser then blocks them); same-origin and non-browser requests pass.
func (c Config) CORS(o CORSOptions) func(http.Handler) http.Handler {
	origins := make(map[string]bool, len(o.AllowedOrigins))
	for _, s := range o.AllowedOrigins {
		if s = strings.TrimRight(strings.TrimSpace(s), "/"); s != "" && s != "*" {
			origins[strings.ToLower(s)] = true
		}
	}
	methods := o.AllowedMethods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	}
	headers := strings.Join(c.AllowedHeaders(o.ExtraHeaders...), ", ")
	maxAge := o.MaxAge
	if maxAge == 0 {
		maxAge = 300
	}
	expose := o.ExposeHeaders
	if expose == nil {
		expose = DefaultExposeHeaders
	}
	exposed := strings.Join(expose, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			if !origins[strings.ToLower(origin)] {
				if preflight {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if !preflight {
				if exposed != "" {
					w.Header().Set("Access-Control-Expose-Headers", exposed)
				}
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
			w.Header().Set("Access-Control-Allow-Headers", headers)
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(maxAge))
			w.WriteHeader(http.StatusNoContent)
		})
	}
}
