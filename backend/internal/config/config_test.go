package config

import (
	"strings"
	"testing"
	"time"
)

// devDenKey is a dev-only DEN_LINK_ENCRYPTION_KEY (32 bytes, base64).
const devDenKey = "ZGVzay1kZXYtb25seS1kZW4tbGluay1rZXktMzJieXQ="

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":            "postgres://desk:desk@localhost:5440/desk",
		"S3_ENDPOINT":             "http://localhost:9000",
		"S3_BUCKET":               "desk-data-dev",
		"S3_ACCESS_KEY_ID":        "dev",
		"S3_SECRET_ACCESS_KEY":    "dev-only-secret",
		"OIDC_ISSUER_URL":         "https://auth.conservices.test/realms/conservices",
		"OIDC_CLIENT_ID":          "desk-spa",
		"DEN_LINK_ENCRYPTION_KEY": devDenKey,
	}
}

func TestSafeDefaults(t *testing.T) {
	env := baseEnv()
	env["DESK_SECRET_KEY"] = strings.Repeat("k", 32)
	cfg, err := loadWith(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "production" || cfg.AuthMode != AuthModeOIDC || cfg.IsDevelopment() {
		t.Fatalf("defaults: APP_ENV %q AUTH_MODE %q", cfg.AppEnv, cfg.AuthMode)
	}
	if cfg.Audience() != "desk-api" || cfg.GroupMatch.ClaimName() != "groups" {
		t.Fatalf("audience %q claim %q", cfg.Audience(), cfg.GroupMatch.ClaimName())
	}
	if d := cfg.DenConfig("1.0.0"); d.Slug != "desk" || d.Version != "1.0.0" || d.EncryptionKey != devDenKey {
		t.Fatalf("den config %+v", d)
	}

	env["AUTH_MODE"] = "dev"
	mustFail(t, env, "AUTH_MODE=dev")

	delete(env, "AUTH_MODE")
	delete(env, "DEN_LINK_ENCRYPTION_KEY")
	mustFail(t, env, "DEN_LINK_ENCRYPTION_KEY is required")

	env["DEN_LINK_ENCRYPTION_KEY"] = devDenKey
	env["OIDC_GROUP_MATCH"] = "some"
	mustFail(t, env, "OIDC_GROUP_MATCH")

	delete(env, "OIDC_GROUP_MATCH")
	env["HR_SERVICE_TOKEN"] = "too-short"
	mustFail(t, env, "HR_SERVICE_TOKEN must be at least 32")

	env["HR_SERVICE_TOKEN"] = strings.Repeat("h", 32)
	env["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/99"
	mustFail(t, env, "TRUSTED_PROXY_CIDRS")
}

func loadWith(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	return load(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
}

func mustFail(t *testing.T, env map[string]string, want string) {
	t.Helper()
	_, err := loadWith(t, env)
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %q", want, err)
	}
}

func TestDevKeyNeedsExplicitDevelopment(t *testing.T) {
	env := baseEnv()
	// APP_ENV unset is production: the built-in key must not be picked.
	mustFail(t, env, "DESK_SECRET_KEY is required")

	env["APP_ENV"] = "development"
	cfg, err := loadWith(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretKey != DevSecretKey {
		t.Fatalf("expected the dev key, got %q", cfg.SecretKey)
	}
}

func TestDevKeyRefusedInProduction(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["DESK_SECRET_KEY"] = DevSecretKey
	mustFail(t, env, "built-in development key")

	env["DESK_SECRET_KEY"] = "short"
	mustFail(t, env, "at least 32 characters")

	env["DESK_SECRET_KEY"] = strings.Repeat("k", 32)
	if _, err := loadWith(t, env); err != nil {
		t.Fatal(err)
	}
}

func TestPreviousKeyMustDiffer(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["DESK_SECRET_KEY"] = strings.Repeat("a", 32)
	env["DESK_SECRET_KEY_PREVIOUS"] = strings.Repeat("b", 32) + "," + strings.Repeat("a", 32)
	mustFail(t, env, "must not contain the current")
}

func TestPlaintextSMTPOnlyInDevelopment(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["DESK_SECRET_KEY"] = strings.Repeat("k", 32)
	env["SMTP_SECURITY"] = "none"
	mustFail(t, env, "SMTP_SECURITY=none")

	env["APP_ENV"] = "development"
	if _, err := loadWith(t, env); err != nil {
		t.Fatal(err)
	}

	env["SMTP_SECURITY"] = "ssl"
	mustFail(t, env, "SMTP_SECURITY must be tls or starttls")
}

func TestFetchInterval(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "development"
	for value, want := range map[string]string{
		"0s":    "FETCH_INTERVAL must be at least",
		"-5s":   "FETCH_INTERVAL must be at least",
		"1s":    "FETCH_INTERVAL must be at least",
		"often": "FETCH_INTERVAL must be a duration",
	} {
		env["FETCH_INTERVAL"] = value
		mustFail(t, env, want)
	}
	env["FETCH_INTERVAL"] = "2m"
	cfg, err := loadWith(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FetchInterval != 2*time.Minute {
		t.Fatalf("got %s", cfg.FetchInterval)
	}
}

func TestBadNumbersAreErrors(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "development"
	env["SMTP_PORT"] = "abc"
	mustFail(t, env, "SMTP_PORT must be a whole number")
	env["SMTP_PORT"] = "70000"
	mustFail(t, env, "SMTP_PORT must be between")
}

func TestAllowedMailHosts(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "development"
	env["DESK_ALLOWED_MAIL_HOSTS"] = "mail.internal, 10.20.0.0/16"
	cfg, err := loadWith(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedMailHosts) != 2 || cfg.AllowedMailHosts[0] != "mail.internal" {
		t.Fatalf("got %v", cfg.AllowedMailHosts)
	}
	env["DESK_ALLOWED_MAIL_HOSTS"] = "10.20.0.0/99"
	mustFail(t, env, "not a valid CIDR")
}

func TestS3Required(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "development"
	delete(env, "S3_ENDPOINT")
	mustFail(t, env, "S3_ENDPOINT is required")
	env["S3_ENDPOINT"] = "localhost:9000"
	mustFail(t, env, "S3_ENDPOINT must be an http")
}

func TestGoogle(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["DESK_SECRET_KEY"] = strings.Repeat("k", 32)
	env["GOOGLE_WORKSPACE_DOMAIN"] = "example.org"
	mustFail(t, env, "GOOGLE_WORKSPACE_CREDENTIALS_FILE")

	env["GOOGLE_SERVICE_ACCOUNT_FILE"] = "/etc/desk-google/service-account.json"
	mustFail(t, env, "GOOGLE_SERVICE_ACCOUNT_FILE was renamed to GOOGLE_WORKSPACE_CREDENTIALS_FILE")
	delete(env, "GOOGLE_SERVICE_ACCOUNT_FILE")

	env["GOOGLE_WORKSPACE_CREDENTIALS_FILE"] = "/etc/desk-google/service-account.json"
	env["GOOGLE_WORKSPACE_ADMIN_EMAIL"] = "admin@other.org"
	mustFail(t, env, "is not in GOOGLE_WORKSPACE_DOMAIN")

	env["GOOGLE_WORKSPACE_ADMIN_EMAIL"] = "admin@example.org"
	env["GOOGLE_SHARED_MAILBOX"] = "desk@other.org"
	mustFail(t, env, "GOOGLE_SHARED_MAILBOX must be an address in")

	env["GOOGLE_SHARED_MAILBOX"] = "Desk@Example.org"
	cfg, err := loadWith(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoogleSharedMailbox != "desk@example.org" || cfg.GoogleLabel != "Desk" {
		t.Fatalf("got %q %q", cfg.GoogleSharedMailbox, cfg.GoogleLabel)
	}

	env["GOOGLE_FAKE"] = "true"
	mustFail(t, env, "GOOGLE_FAKE is only allowed")
}

func TestErrorsAreAggregated(t *testing.T) {
	_, err := loadWith(t, map[string]string{"APP_ENV": "production"})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"DATABASE_URL", "DESK_SECRET_KEY", "S3_ENDPOINT", "S3_BUCKET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s in %q", want, err)
		}
	}
}
