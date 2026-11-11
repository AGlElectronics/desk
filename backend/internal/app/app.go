// Package app wires configuration, the database pool, auth and HTTP into a
// runnable server. cmd/server/main.go just calls Run.
//
// This package does NOT run Goose migrations on start. Migrations are a
// one-shot controlled operation handled by cmd/migrate (Compose service /
// K8s Job), same as every other Con Services module.
package app

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/brand"
	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/db"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	httpx "github.com/AGlElectronics/desk/backend/internal/http"
	"github.com/AGlElectronics/desk/backend/internal/mailprovider"
	"github.com/AGlElectronics/desk/backend/internal/secretbox"
	"github.com/AGlElectronics/desk/backend/internal/tlscert"
)

// Version is set via -ldflags at build time; left as "dev" for local runs.
var Version = "dev"

// Run boots the server and blocks until a shutdown signal is received or
// ListenAndServe fails.
func Run(ctx context.Context, logger zerolog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger.Info().
		Str("env", cfg.AppEnv).
		Str("addr", cfg.HTTPAddr).
		Str("auth_mode", string(cfg.AuthMode)).
		Str("version", Version).
		Msg("starting desk backend")

	// DB pool. Migrations are NOT applied here; cmd/migrate ran first.
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := db.Open(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pool.Close()
	logger.Info().Msg("database pool ready")

	// OIDC verifier. Only required when AUTH_MODE=oidc; dev mode skips.
	var oidcVerifier *auth.OIDCVerifier
	if cfg.AuthMode == config.AuthModeOIDC {
		oidcCtx, cancelOIDC := context.WithTimeout(ctx, 30*time.Second)
		oidcVerifier, err = auth.NewOIDCVerifier(oidcCtx, cfg)
		cancelOIDC()
		if err != nil {
			return fmt.Errorf("init oidc verifier: %w", err)
		}
		logger.Info().Str("issuer", cfg.OIDCIssuerURL).Msg("oidc verifier ready")
	}

	// Access config catalog. A bogus ACCESS_CONFIG_PATH fails loudly; no
	// file at all falls back to the built-in role catalog (dev only).
	var access *auth.AccessConfig
	if cfg.AccessConfigPath != "" {
		access, err = auth.LoadAccessConfig(cfg.AccessConfigPath)
		if err != nil {
			return fmt.Errorf("load access config: %w", err)
		}
		logger.Info().
			Str("path", cfg.AccessConfigPath).
			Strs("roles", access.Roles()).
			Msg("access config loaded")
	} else {
		access = auth.DefaultAccessConfig()
		logger.Info().Msg("no access config file configured; using built-in role catalog")
	}

	brandProvider := brand.New(cfg)
	den := denlink.New(pool, denlink.Config{Slug: "desk", Version: Version, InternalURL: cfg.DenInternalURL, Mode: cfg.DenMode}, logger)
	if err := den.Restore(ctx); err != nil {
		logger.Warn().Err(err).Msg("den link: load failed")
	}
	go den.Run(ctx)

	deskSvc, err := newDeskService(ctx, pool, cfg)
	if err != nil {
		return err
	}
	logger.Info().Str("key_id", deskSvc.KeyID()).Int("previous_keys", len(cfg.PreviousSecretKeys)).Msg("secret key loaded")
	// One replica: the fetcher assumes nobody else polls the same mailboxes.
	// The workers stop before the pool closes (see runServer).
	workCtx, stopWork := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWork()
	var workers sync.WaitGroup
	workers.Go(func() { deskSvc.RunFetcher(workCtx, cfg.FetchInterval, logger) })
	workers.Go(func() { deskSvc.RunSender(workCtx, logger) })
	workers.Go(func() { deskSvc.RunMaintenance(workCtx, logger) })
	stopWorkers := func() {
		stopWork()
		workers.Wait()
	}

	router := httpx.NewRouter(httpx.Deps{
		Desk:         deskSvc,
		Config:       cfg,
		Pool:         pool,
		Logger:       logger,
		Brand:        brandProvider,
		Den:          den,
		OIDCVerifier: oidcVerifier,
		Access:       access,
	}, Version)

	server := &nethttp.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if _, err := tlscert.Configure(ctx, server, logger); err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	return runServer(ctx, server, stopWorkers, cfg.ShutdownTimeout, logger)
}

func newDeskService(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) (*desk.Service, error) {
	keys, err := secretbox.New(cfg.SecretKey, cfg.PreviousSecretKeys...)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	s3ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	blobs, err := blob.NewS3(s3ctx, blob.S3Config{
		Endpoint: cfg.S3Endpoint, Bucket: cfg.S3Bucket, Region: cfg.S3Region, UsePathStyle: cfg.S3UsePathStyle,
		AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey, CreateBucket: cfg.IsDevelopment(),
	})
	if err != nil {
		return nil, fmt.Errorf("object storage: %w", err)
	}
	google, err := googleOptions(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return desk.NewService(pool, desk.Options{
		Blobs:      blobs,
		Google:     google,
		NotifyFrom: cfg.NotifyFrom,
		BaseURL:    cfg.AppBaseURL,
		Keyring: keys,
		SMTP: desk.SMTPSettings{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Security: cfg.SMTPSecurity,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		},
		Development:      cfg.IsDevelopment(),
		AllowedMailHosts: cfg.AllowedMailHosts,
	}), nil
}

// GoogleProvider builds the Google Workspace client (Gmail API and Admin
// SDK with domain-wide delegation) from GOOGLE_*. This version of Desk
// ships without one; the client from shared/go/gworkspace sets it.
var GoogleProvider func(ctx context.Context, cfg *config.Config) (mailprovider.Provider, error)

func googleOptions(ctx context.Context, cfg *config.Config) (*desk.GoogleOptions, error) {
	if !cfg.GoogleEnabled() {
		return nil, nil
	}
	opts := &desk.GoogleOptions{Domain: cfg.GoogleDomain, SharedMailbox: cfg.GoogleSharedMailbox, Label: cfg.GoogleLabel}
	switch {
	case cfg.GoogleFake:
		fake := mailprovider.NewFake()
		if cfg.GoogleSharedMailbox != "" {
			fake.AddUser(cfg.GoogleSharedMailbox)
		}
		opts.Provider = fake
	case GoogleProvider != nil:
		p, err := GoogleProvider(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("google workspace: %w", err)
		}
		opts.Provider = p
	default:
		return nil, errors.New("GOOGLE_WORKSPACE_DOMAIN is set, but this build of Desk has no Google Workspace client yet; " +
			"unset it to use IMAP/SMTP mailboxes, or GOOGLE_FAKE=true to try the flow in development")
	}
	return opts, nil
}

// Reencrypt seals every stored mailbox password under the current
// DESK_SECRET_KEY (`server reencrypt`). It fails when a value cannot be
// opened with DESK_SECRET_KEY or DESK_SECRET_KEY_PREVIOUS.
func Reencrypt(ctx context.Context, logger zerolog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pool.Close()
	svc, err := newDeskService(ctx, pool, cfg)
	if err != nil {
		return err
	}
	res, err := svc.Reencrypt(ctx)
	if err != nil {
		return fmt.Errorf("reencrypt: %w", err)
	}
	logger.Info().Str("key_id", svc.KeyID()).Int("resealed", res.Resealed).Int("already_current", res.Current).
		Strs("failed", res.Failed).Msg("reencrypt finished")
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d stored passwords could not be decrypted; enter them again in Desk", len(res.Failed))
	}
	return nil
}

// runServer serves until ctx is cancelled (SIGINT/SIGTERM in cmd/server),
// then drains HTTP requests and stops the background workers. The caller
// closes the database pool afterwards, so no worker writes to a closed pool.
func runServer(ctx context.Context, server *nethttp.Server, stopWorkers func(), shutdownTimeout time.Duration, logger zerolog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", server.Addr).Msg("http listening")
		if err := tlscert.ListenAndServe(server); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
		logger.Info().Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if serveErr == nil {
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error().Err(err).Msg("http shutdown error")
		}
	}
	done := make(chan struct{})
	go func() {
		stopWorkers()
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		logger.Warn().Msg("background workers did not stop within SHUTDOWN_TIMEOUT")
	}
	logger.Info().Msg("shutdown complete")
	return serveErr
}
