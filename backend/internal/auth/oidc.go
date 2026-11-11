package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/groupmatch"
)

// OIDCVerifier checks the access tokens the web app sends (token contract,
// docs/concepts/authentication.md): signature, issuer, expiry and an
// audience naming Desk's API (OIDC_AUDIENCE, default desk-api). ID tokens
// are refused. Discovery handles JWKS rotation; nothing identity-provider
// specific lives here.
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
	groups   groupmatch.Config
}

// OIDCClaims is the parsed claim subset Desk stores.
type OIDCClaims struct {
	Issuer            string `json:"iss"`
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Nickname          string `json:"nickname"`
	// Type is Keycloak's "typ" claim: "Bearer" for access tokens, "ID" for
	// ID tokens.
	Type string `json:"typ"`
	// Groups come only from OIDC_GROUPS_CLAIM (groupmatch); "roles" is
	// never merged in.
	Groups []string `json:"-"`
}

// NewOIDCVerifier runs OIDC discovery and returns a verifier for Desk's API
// audience. Call once at startup; the result is safe for concurrent use.
func NewOIDCVerifier(ctx context.Context, cfg *config.Config) (*OIDCVerifier, error) {
	if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, errors.New("OIDC verifier requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &OIDCVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.Audience()}),
		issuer:   cfg.OIDCIssuerURL,
		groups:   cfg.GroupMatch,
	}, nil
}

// Verify parses and validates a bearer token. On success the issuer claim
// is the configured one, so downstream code never trusts a client value.
func (v *OIDCVerifier) Verify(ctx context.Context, bearer string) (*OIDCClaims, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, errors.New("missing bearer token")
	}
	token, err := v.verifier.Verify(ctx, bearer)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	var c OIDCClaims
	if err := token.Claims(&c); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	if strings.EqualFold(c.Type, "ID") {
		return nil, errors.New("an ID token was sent; the API needs an access token")
	}
	if c.Subject == "" {
		return nil, errors.New("token missing sub claim")
	}
	var all map[string]any
	if err := token.Claims(&all); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	c.Groups = v.groups.Groups(all)
	c.Issuer = v.issuer
	return &c, nil
}
