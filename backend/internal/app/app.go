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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/brand"
	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/db"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	httpx "github.com/AGlElectronics/desk/backend/internal/http"
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

	deskSvc := desk.NewService(pool, cfg.SecretKey, desk.SMTPSettings{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, Security: cfg.SMTPSecurity,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
	})
	// One replica: the fetcher assumes nobody else polls the same mailboxes.
	go deskSvc.RunFetcher(ctx, cfg.FetchInterval, logger)
	go deskSvc.RunSender(ctx, logger)

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

	return runServer(ctx, server, pool, cfg.ShutdownTimeout, logger)
}

func runServer(ctx context.Context, server *nethttp.Server, pool *pgxpool.Pool, shutdownTimeout time.Duration, logger zerolog.Logger) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", server.Addr).Msg("http listening")
		if err := tlscert.ListenAndServe(server); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-signalCtx.Done():
		logger.Info().Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("http shutdown error")
	}
	pool.Close()
	logger.Info().Msg("shutdown complete")
	return nil
}
