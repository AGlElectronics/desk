package config

import (
	"strings"
	"testing"
	"time"
)

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":         "postgres://desk:desk@localhost:5440/desk",
		"S3_ENDPOINT":          "http://localhost:9000",
		"S3_BUCKET":            "desk-data-dev",
		"S3_ACCESS_KEY_ID":     "dev",
		"S3_SECRET_ACCESS_KEY": "dev-only-secret",
	}
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
	// APP_ENV unset defaults to development, but the built-in key still
	// must not be picked.
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
	mustFail(t, env, "GOOGLE_SERVICE_ACCOUNT_FILE is required")

	env["GOOGLE_SERVICE_ACCOUNT_FILE"] = "/etc/desk-google/service-account.json"
	env["GOOGLE_ADMIN_USER"] = "admin@example.org"
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
