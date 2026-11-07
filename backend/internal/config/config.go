// Package config loads and validates the backend runtime configuration from
// environment variables (12-factor). Validation runs once at startup and
// fails loudly: a misconfigured deployment is better than a silently
// degraded one.
//
// Desk keeps inbound attachments in Postgres in this slice, so there is
// deliberately no S3 configuration here yet (docs/plan.md).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/AGlElectronics/desk/backend/internal/clientip"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
)

// AuthMode selects how incoming requests are authenticated.
//
//   - AuthModeDev reads an X-Dev-User header (identity) and an X-Dev-Groups
//     header (simulated IdP groups) and provisions a local user tied to the
//     "dev" issuer. Only safe for local scaffolding and CI.
//   - AuthModeOIDC verifies a Bearer JWT via the configured issuer.
type AuthMode string

const (
	AuthModeOIDC AuthMode = "oidc"
	AuthModeDev  AuthMode = "dev"
)

// Config is the validated process configuration.
type Config struct {
	// Core
	AppEnv     string
	AppBaseURL string
	HTTPAddr   string

	// Database
	DatabaseURL string

	// Auth
	AuthMode         AuthMode
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCAudience     string
	AccessConfigPath string
	DevDefaultUser   string
	DevDefaultGroups []string

	// Brand provider
	BrandLocalPath string // optional brand.json; Den's brand overlays it once paired
	// DenInternalURL replaces the host of pasted Den pairing URLs (Docker).
	DenInternalURL string
	// DenMode is DEN_MODE: required keeps the API locked until the module
	// is paired with Den; standalone (the default) does not.
	DenMode denlink.Mode

	// HTTP
	CORSAllowedOrigins []string
	// TrustedProxyCIDRs lists the proxies whose X-Forwarded-For entries are
	// believed when working out the client IP (TRUSTED_PROXY_CIDRS).
	TrustedProxyCIDRs []netip.Prefix
	ShutdownTimeout   time.Duration

	// Mail
	// SecretKey encrypts mailbox IMAP/SMTP passwords at rest.
	SecretKey string
	// FetchInterval is how often every mailbox is polled over IMAP.
	FetchInterval time.Duration
	// Module-wide SMTP for mailboxes without their own SMTP host.
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	SMTPUsername string
	SMTPPassword string
}

// Load reads environment variables (plus an optional .env file) and returns
// a validated Config. Required fields that are missing return an aggregated
// error so operators see every problem at once instead of fixing one at a
// time.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv: getEnv("APP_ENV", "development"),
		// Container-internal defaults: every module in the ecosystem binds
		// :8080 inside its container. Host-side port mapping (host 8150 ->
		// container 8080 for Desk) is handled by docker-compose / K8s.
		AppBaseURL: getEnv("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:   getEnv("HTTP_ADDR", ":8080"),

		DatabaseURL: getEnv("DATABASE_URL", ""),

		AuthMode:         AuthMode(getEnv("AUTH_MODE", "dev")),
		OIDCIssuerURL:    getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", ""),
		OIDCAudience:     getEnv("OIDC_AUDIENCE", ""),
		AccessConfigPath: getEnv("ACCESS_CONFIG_PATH", ""),
		DevDefaultUser:   getEnv("DEV_DEFAULT_USER", "dev-admin"),
		DevDefaultGroups: splitCSV(getEnv("DEV_DEFAULT_GROUPS", "desk-admin")),

		BrandLocalPath: getEnv("BRAND_LOCAL_PATH", ""),
		DenInternalURL: getEnv("DEN_INTERNAL_URL", ""),
		DenMode:        denlink.Mode(getEnv("DEN_MODE", string(denlink.ModeStandalone))),

		CORSAllowedOrigins: splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3008")),
		ShutdownTimeout:    getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		SecretKey:     getEnv("DESK_SECRET_KEY", ""),
		FetchInterval: getEnvDuration("FETCH_INTERVAL", 60*time.Second),
		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnvInt("SMTP_PORT", 587),
		SMTPSecurity:  getEnv("SMTP_SECURITY", "starttls"),
		SMTPUsername:  getEnv("SMTP_USERNAME", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
	}
	if cfg.SecretKey == "" && cfg.AppEnv == "development" {
		cfg.SecretKey = "desk-dev-secret-key-do-not-use-in-production"
	}

	trusted, err := clientip.ParseCIDRs(getEnv("TRUSTED_PROXY_CIDRS", clientip.DefaultTrustedProxies))
	if err != nil {
		return nil, fmt.Errorf("invalid config: TRUSTED_PROXY_CIDRS: %w", err)
	}
	cfg.TrustedProxyCIDRs = trusted

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if err := c.DenMode.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	if c.SecretKey == "" {
		errs = append(errs, "DESK_SECRET_KEY is required outside APP_ENV=development")
	}
	switch c.SMTPSecurity {
	case "tls", "starttls", "none":
	default:
		errs = append(errs, fmt.Sprintf("SMTP_SECURITY must be tls, starttls or none, got %q", c.SMTPSecurity))
	}

	switch c.AuthMode {
	case AuthModeOIDC:
		if c.OIDCIssuerURL == "" {
			errs = append(errs, "OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		if c.OIDCClientID == "" {
			errs = append(errs, "OIDC_CLIENT_ID is required when AUTH_MODE=oidc")
		}
	case AuthModeDev:
		// dev mode: no extra required fields; dev users auto-provision.
	default:
		errs = append(errs, fmt.Sprintf("AUTH_MODE must be 'oidc' or 'dev', got %q", c.AuthMode))
	}

	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
