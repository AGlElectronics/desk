// Code generated from con-services shared/go/denlink; DO NOT EDIT. Run scripts/sync-shared.

package denlink

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Headers Den sends on a call it makes into a module (GET /api/portal/todos).
// Den's backend sets them; the person's own token never leaves Den, and
// the person is not taken from the query string.
const (
	HeaderUserIssuer  = "X-Den-User-Issuer"
	HeaderUserSubject = "X-Den-User-Subject"
)

// maxDenUserLen caps each identity header. Den's subjects are identity
// provider ids; 512 bytes is enough and rejects a padded request.
const maxDenUserLen = 512

// PortalCaller is the person Den asks about.
type PortalCaller struct {
	Issuer  string
	Subject string
}

type portalCallerKey struct{}

// BearerToken returns the token from an Authorization header, or "" when
// the header is missing or is not Bearer. The scheme is case-insensitive.
func BearerToken(header string) string {
	header = strings.TrimSpace(header)
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// CredentialMatches reports whether token is the pairing credential Den
// issued to this module. A nil client, a missing link, a revoked link, an
// empty token, or a credential that cannot be decrypted does not match.
// The comparison hashes both sides and then compares the digests, so it
// does not stop at the first different byte and does not depend on the
// token being the same length as the stored credential.
func (c *Client) CredentialMatches(ctx context.Context, token string) bool {
	if c == nil || c.pool == nil {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	l, err := c.load(ctx)
	if err != nil || l == nil || l.credential == "" || l.revokedAt != nil {
		return false
	}
	return credentialEqual(token, l.credential)
}

func credentialEqual(got, want string) bool {
	sumGot := sha256.Sum256([]byte(got))
	sumWant := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(sumGot[:], sumWant[:]) == 1
}

// UserFromDen reads the person Den names in HeaderUserIssuer and
// HeaderUserSubject. ok is false when either is missing, blank, or longer
// than 512 bytes. Values are trimmed. Query parameters are ignored: Den
// does not send the person there.
func UserFromDen(r *http.Request) (issuer, subject string, ok bool) {
	if r == nil {
		return "", "", false
	}
	issuer = strings.TrimSpace(r.Header.Get(HeaderUserIssuer))
	subject = strings.TrimSpace(r.Header.Get(HeaderUserSubject))
	if issuer == "" || subject == "" || len(issuer) > maxDenUserLen || len(subject) > maxDenUserLen {
		return "", "", false
	}
	return issuer, subject, true
}

// PortalCallerFrom returns the person RequireDen accepted.
func PortalCallerFrom(ctx context.Context) (PortalCaller, bool) {
	if ctx == nil {
		return PortalCaller{}, false
	}
	u, ok := ctx.Value(portalCallerKey{}).(PortalCaller)
	return u, ok
}

// RequireDen allows next only when the request is from Den. Authorization
// must be this module's pairing credential (CredentialMatches), and
// UserFromDen must name the person. The person is then available from
// PortalCallerFrom.
//
// A missing link, a revoked credential, a database error and a wrong token
// all answer 401 unauthorized with "invalid or revoked credential", so the
// route does not say which. Missing identity headers answer 400 bad_request
// with "issuer and subject are required".
//
// Mount it on GET /api/portal/todos. Den calls that path itself.
func (c *Client) RequireDen(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.CredentialMatches(r.Context(), BearerToken(r.Header.Get("Authorization"))) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked credential")
			return
		}
		issuer, subject, ok := UserFromDen(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "issuer and subject are required")
			return
		}
		ctx := context.WithValue(r.Context(), portalCallerKey{}, PortalCaller{Issuer: issuer, Subject: subject})
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
