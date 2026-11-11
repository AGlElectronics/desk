// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

// Package authbase holds the authentication and HTTP hardening pieces every
// Con Services backend shares: the AUTH_MODE/APP_ENV guard, CORS headers,
// a bounded JSON decoder, a rate limiter, API security headers and the
// person's identity key.
package authbase

import (
	"fmt"
	"strings"
)

// AuthMode is AUTH_MODE.
type AuthMode string

const (
	// AuthOIDC verifies OIDC tokens (the default).
	AuthOIDC AuthMode = "oidc"
	// AuthDev trusts the X-Dev-User and X-Dev-Groups headers. Only allowed
	// with APP_ENV=development.
	AuthDev AuthMode = "dev"
)

// Environment variable names and defaults read by FromEnv.
const (
	EnvAuthMode = "AUTH_MODE"
	EnvAppEnv   = "APP_ENV"

	DefaultAuthMode = AuthOIDC
	DefaultAppEnv   = "production"
	// Development is the APP_ENV value that allows dev conveniences.
	Development = "development"
)

// Config is the authentication guard's settings.
type Config struct {
	// AppEnv is APP_ENV; empty means production.
	AppEnv string
	// AuthMode is AUTH_MODE; empty means oidc.
	AuthMode AuthMode
	// TrustedProxies is TRUSTED_PROXY_CIDRS: the proxies whose
	// X-Forwarded-For is believed (see TrustedProxies.ClientAddr). The zero
	// value trusts nobody.
	TrustedProxies TrustedProxies
}

// FromEnv reads AUTH_MODE (default oidc), APP_ENV (default production) and
// TRUSTED_PROXY_CIDRS (default DefaultTrustedProxyCIDRs, "none" for no
// proxy) through getenv and validates them. A backend must refuse to start
// when it returns an error.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		AppEnv:   NormalizeAppEnv(getenv(EnvAppEnv)),
		AuthMode: AuthMode(strings.ToLower(strings.TrimSpace(getenv(EnvAuthMode)))),
	}
	if c.AuthMode == "" {
		c.AuthMode = DefaultAuthMode
	}
	proxies := getenv(EnvTrustedProxies)
	if strings.TrimSpace(proxies) == "" {
		proxies = DefaultTrustedProxyCIDRs
	}
	tp, err := ParseTrustedProxies(proxies)
	if err != nil {
		return c, err
	}
	c.TrustedProxies = tp
	return c, c.Validate()
}

// Validate rejects an unknown AUTH_MODE and AUTH_MODE=dev outside
// APP_ENV=development.
func (c Config) Validate() error {
	switch c.mode() {
	case AuthOIDC:
		return nil
	case AuthDev:
		if !c.Development() {
			return fmt.Errorf("%s=dev lets anyone sign in as anyone and is only allowed with %s=%s (got %s=%q)",
				EnvAuthMode, EnvAppEnv, Development, EnvAppEnv, c.AppEnv)
		}
		return nil
	}
	return fmt.Errorf("%s must be %q or %q, got %q", EnvAuthMode, AuthOIDC, AuthDev, string(c.AuthMode))
}

func (c Config) mode() AuthMode {
	if c.AuthMode == "" {
		return DefaultAuthMode
	}
	return c.AuthMode
}

// Development reports APP_ENV=development.
func (c Config) Development() bool { return NormalizeAppEnv(c.AppEnv) == Development }

// NormalizeAppEnv trims and lower-cases an APP_ENV value; empty becomes
// production. denlink has an identical copy (the packages never import each
// other), so both read APP_ENV the same way.
func NormalizeAppEnv(s string) string {
	if s = strings.ToLower(strings.TrimSpace(s)); s == "" {
		return DefaultAppEnv
	}
	return s
}

// DevAuth reports AUTH_MODE=dev in a valid (development) configuration.
func (c Config) DevAuth() bool { return c.mode() == AuthDev && c.Development() }
