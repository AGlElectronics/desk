package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/AGlElectronics/desk/backend/internal/config"
)

// OIDCVerifier wraps the go-oidc verifier and exposes the subset of token
// claims Desk cares about. The verifier handles JWKS rotation via OIDC
// discovery; nothing identity-provider-specific lives in this code.
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
}

// OIDCClaims is the parsed claim subset Desk stores. We try the standard
// claim names first and fall back to common Keycloak shapes.
type OIDCClaims struct {
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Nickname          string   `json:"nickname"`
	Groups            []string `json:"groups"`
	Roles             []string `json:"roles"`
}

// NewOIDCVerifier runs OIDC discovery and returns a verifier configured for
// Desk's audience. Call once at startup; the returned object is safe for
// concurrent use.
func NewOIDCVerifier(ctx context.Context, cfg *config.Config) (*OIDCVerifier, error) {
	if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, errors.New("OIDC verifier requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	audience := cfg.OIDCAudience
	if audience == "" {
		audience = cfg.OIDCClientID
	}
	return &OIDCVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
		issuer:   cfg.OIDCIssuerURL,
	}, nil
}

// Verify parses and validates a bearer token. On success the issuer claim
// is populated from the verifier config so downstream code never has to
// trust a client-sent value.
func (v *OIDCVerifier) Verify(ctx context.Context, bearer string) (*OIDCClaims, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, errors.New("missing bearer token")
	}
	idToken, err := v.verifier.Verify(ctx, bearer)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	var c OIDCClaims
	if err := idToken.Claims(&c); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	if c.Subject == "" {
		return nil, errors.New("token missing sub claim")
	}
	// Pin the issuer to what the verifier was configured with. Keycloak
	// token issuers include trailing /realm paths; always use what we
	// trust, not what the token asserts.
	c.Issuer = v.issuer
	// Many IdPs expose `roles` rather than `groups`; treat both as role-ish
	// strings for the access config binding lookup.
	c.Groups = mergeUnique(c.Groups, c.Roles)
	return &c, nil
}

func mergeUnique(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
