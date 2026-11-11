// Package config loads and validates the backend runtime configuration from
// environment variables (12-factor). Validation runs once at startup and
// fails loudly: a misconfigured deployment is better than a silently
// degraded one.
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
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

// EnvDevelopment is the only APP_ENV that unlocks development shortcuts
// (built-in secret key, plaintext mail connections, the fake Google
// Workspace).
const EnvDevelopment = "development"

// DevSecretKey is used when DESK_SECRET_KEY is unset and APP_ENV is
// explicitly development. Never valid anywhere else.
const DevSecretKey = "desk-dev-secret-key-do-not-use-in-production"

// MinFetchInterval is the shortest accepted FETCH_INTERVAL.
const MinFetchInterval = 5 * time.Second

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

	// Secrets at rest
	// SecretKey encrypts mailbox passwords (DESK_SECRET_KEY).
	SecretKey string
	// PreviousSecretKeys still decrypt, so a rotated key keeps working
	// until `server reencrypt` has run (DESK_SECRET_KEY_PREVIOUS).
	PreviousSecretKeys []string

	// Mail
	// FetchInterval is how often every mailbox is polled.
	FetchInterval time.Duration
	// AllowedMailHosts are host names and CIDRs that may resolve to private,
	// loopback or link-local addresses (DESK_ALLOWED_MAIL_HOSTS).
	AllowedMailHosts []string
	// Module-wide SMTP for IMAP mailboxes without their own SMTP host, and
	// for notifications when there is no Google Workspace.
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	SMTPUsername string
	SMTPPassword string
	// NotifyFrom is the From address of Desk's own notifications.
	NotifyFrom string

	// Google Workspace connector (empty domain: off).
	GoogleDomain             string
	GoogleServiceAccountFile string
	GoogleAdminUser          string
	GoogleSharedMailbox      string
	GoogleLabel              string
	// GoogleFake uses an in-memory Workspace (development only).
	GoogleFake bool

	// Object storage for attachments and raw mail.
	S3Endpoint        string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Region          string
	S3UsePathStyle    bool
}

// IsDevelopment reports whether APP_ENV is development.
func (c *Config) IsDevelopment() bool { return c.AppEnv == EnvDevelopment }

// GoogleEnabled reports whether the Google Workspace connector is configured.
func (c *Config) GoogleEnabled() bool { return c.GoogleDomain != "" }

// Load reads environment variables (plus an optional .env file) and returns
// a validated Config. Every problem is reported at once.
func Load() (*Config, error) {
	_ = godotenv.Load()
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (*Config, error) {
	var errs []string
	env := func(key, fallback string) string {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return fallback
	}
	duration := func(key string, fallback time.Duration) time.Duration {
		v := env(key, "")
		if v == "" {
			return fallback
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s must be a duration such as 60s or 5m, got %q", key, v))
			return fallback
		}
		return d
	}
	integer := func(key string, fallback int) int {
		v := env(key, "")
		if v == "" {
			return fallback
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s must be a whole number, got %q", key, v))
			return fallback
		}
		return n
	}
	boolean := func(key string, fallback bool) bool {
		v := env(key, "")
		if v == "" {
			return fallback
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s must be true or false, got %q", key, v))
			return fallback
		}
		return b
	}

	cfg := &Config{
		AppEnv: env("APP_ENV", EnvDevelopment),
		// Container-internal defaults: every module in the ecosystem binds
		// :8080 inside its container. Host-side port mapping (host 8150 ->
		// container 8080 for Desk) is handled by docker-compose / K8s.
		AppBaseURL: env("APP_BASE_URL", "http://localhost:8080"),
		HTTPAddr:   env("HTTP_ADDR", ":8080"),

		DatabaseURL: env("DATABASE_URL", ""),

		AuthMode:         AuthMode(env("AUTH_MODE", "dev")),
		OIDCIssuerURL:    env("OIDC_ISSUER_URL", ""),
		OIDCClientID:     env("OIDC_CLIENT_ID", ""),
		OIDCAudience:     env("OIDC_AUDIENCE", ""),
		AccessConfigPath: env("ACCESS_CONFIG_PATH", ""),
		DevDefaultUser:   env("DEV_DEFAULT_USER", "dev-admin"),
		DevDefaultGroups: splitCSV(env("DEV_DEFAULT_GROUPS", "desk-admin")),

		BrandLocalPath: env("BRAND_LOCAL_PATH", ""),
		DenInternalURL: env("DEN_INTERNAL_URL", ""),
		DenMode:        denlink.Mode(env("DEN_MODE", string(denlink.ModeStandalone))),

		CORSAllowedOrigins: splitCSV(env("CORS_ALLOWED_ORIGINS", "http://localhost:3008")),
		ShutdownTimeout:    duration("SHUTDOWN_TIMEOUT", 15*time.Second),

		SecretKey:          env("DESK_SECRET_KEY", ""),
		PreviousSecretKeys: splitCSV(env("DESK_SECRET_KEY_PREVIOUS", "")),

		FetchInterval:    duration("FETCH_INTERVAL", 60*time.Second),
		AllowedMailHosts: splitCSV(env("DESK_ALLOWED_MAIL_HOSTS", "")),
		SMTPHost:         env("SMTP_HOST", ""),
		SMTPPort:         integer("SMTP_PORT", 587),
		SMTPSecurity:     env("SMTP_SECURITY", "starttls"),
		SMTPUsername:     env("SMTP_USERNAME", ""),
		SMTPPassword:     env("SMTP_PASSWORD", ""),
		NotifyFrom:       env("NOTIFY_FROM", ""),

		GoogleDomain:             strings.ToLower(env("GOOGLE_WORKSPACE_DOMAIN", "")),
		GoogleServiceAccountFile: env("GOOGLE_SERVICE_ACCOUNT_FILE", ""),
		GoogleAdminUser:          env("GOOGLE_ADMIN_USER", ""),
		GoogleSharedMailbox:      strings.ToLower(env("GOOGLE_SHARED_MAILBOX", "")),
		GoogleLabel:              env("GOOGLE_LABEL", "Desk"),
		GoogleFake:               boolean("GOOGLE_FAKE", false),

		S3Endpoint:        env("S3_ENDPOINT", ""),
		S3Bucket:          env("S3_BUCKET", ""),
		S3AccessKeyID:     env("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey: env("S3_SECRET_ACCESS_KEY", ""),
		S3Region:          env("S3_REGION", "us-east-1"),
		S3UsePathStyle:    boolean("S3_USE_PATH_STYLE", true),
	}

	// The built-in key needs APP_ENV=development set on purpose: a missing
	// APP_ENV must never select it.
	if appEnv, _ := lookup("APP_ENV"); cfg.SecretKey == "" && strings.TrimSpace(appEnv) == EnvDevelopment {
		cfg.SecretKey = DevSecretKey
	}

	trusted, err := clientip.ParseCIDRs(env("TRUSTED_PROXY_CIDRS", clientip.DefaultTrustedProxies))
	if err != nil {
		errs = append(errs, "TRUSTED_PROXY_CIDRS: "+err.Error())
	}
	cfg.TrustedProxyCIDRs = trusted

	errs = append(errs, cfg.validate()...)
	if len(errs) > 0 {
		return nil, errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return cfg, nil
}

func (c *Config) validate() []string {
	var errs []string
	dev := c.IsDevelopment()

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if err := c.DenMode.Validate(); err != nil {
		errs = append(errs, err.Error())
	}

	switch {
	case c.SecretKey == "":
		errs = append(errs, "DESK_SECRET_KEY is required (the built-in key needs APP_ENV=development set explicitly)")
	case c.SecretKey == DevSecretKey && !dev:
		errs = append(errs, "DESK_SECRET_KEY is the built-in development key; set your own outside APP_ENV=development")
	case len(c.SecretKey) < 32 && !dev:
		errs = append(errs, "DESK_SECRET_KEY must be at least 32 characters (openssl rand -base64 32)")
	}
	for _, k := range c.PreviousSecretKeys {
		if k == c.SecretKey {
			errs = append(errs, "DESK_SECRET_KEY_PREVIOUS must not contain the current DESK_SECRET_KEY")
		}
	}

	if c.FetchInterval < MinFetchInterval {
		errs = append(errs, fmt.Sprintf("FETCH_INTERVAL must be at least %s, got %s", MinFetchInterval, c.FetchInterval))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, "SHUTDOWN_TIMEOUT must be positive")
	}

	switch c.SMTPSecurity {
	case "tls", "starttls":
	case "none":
		if !dev {
			errs = append(errs, "SMTP_SECURITY=none (plaintext) is only allowed with APP_ENV=development")
		}
	default:
		errs = append(errs, fmt.Sprintf("SMTP_SECURITY must be tls or starttls, got %q", c.SMTPSecurity))
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		errs = append(errs, fmt.Sprintf("SMTP_PORT must be between 1 and 65535, got %d", c.SMTPPort))
	}
	for _, h := range c.AllowedMailHosts {
		if strings.Contains(h, "/") {
			if _, err := netip.ParsePrefix(h); err != nil {
				errs = append(errs, fmt.Sprintf("DESK_ALLOWED_MAIL_HOSTS: %q is not a valid CIDR", h))
			}
		}
	}
	if c.NotifyFrom != "" {
		if _, err := mail.ParseAddress(c.NotifyFrom); err != nil {
			errs = append(errs, fmt.Sprintf("NOTIFY_FROM must be an email address such as \"Desk <desk@example.org>\", got %q", c.NotifyFrom))
		}
	}

	if c.GoogleFake && !dev {
		errs = append(errs, "GOOGLE_FAKE is only allowed with APP_ENV=development")
	}
	if c.GoogleEnabled() {
		if !c.GoogleFake && c.GoogleServiceAccountFile == "" {
			errs = append(errs, "GOOGLE_SERVICE_ACCOUNT_FILE is required when GOOGLE_WORKSPACE_DOMAIN is set")
		}
		if !c.GoogleFake && c.GoogleAdminUser == "" {
			errs = append(errs, "GOOGLE_ADMIN_USER is required when GOOGLE_WORKSPACE_DOMAIN is set")
		}
		if c.GoogleSharedMailbox != "" && !strings.HasSuffix(c.GoogleSharedMailbox, "@"+c.GoogleDomain) {
			errs = append(errs, "GOOGLE_SHARED_MAILBOX must be an address in GOOGLE_WORKSPACE_DOMAIN")
		}
		if strings.TrimSpace(c.GoogleLabel) == "" {
			errs = append(errs, "GOOGLE_LABEL must not be empty")
		}
	} else if c.GoogleFake {
		errs = append(errs, "GOOGLE_FAKE needs GOOGLE_WORKSPACE_DOMAIN")
	}

	switch {
	case c.S3Endpoint == "":
		errs = append(errs, "S3_ENDPOINT is required (attachments and raw mail are stored in S3)")
	default:
		if u, err := url.Parse(c.S3Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Sprintf("S3_ENDPOINT must be an http:// or https:// URL, got %q", c.S3Endpoint))
		}
	}
	if c.S3Bucket == "" {
		errs = append(errs, "S3_BUCKET is required")
	}
	if c.S3AccessKeyID == "" || c.S3SecretAccessKey == "" {
		errs = append(errs, "S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required")
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
	return errs
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
