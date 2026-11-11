// Package main is the Desk API server entrypoint.
//
// Keep this file tiny: it just configures the root logger and hands off to
// internal/app.Run. Migrations are NOT applied here — see cmd/migrate for
// the dedicated one-shot migrator that runs before the API in Compose/K8s.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/app"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.TimeFieldFormat = time.RFC3339

	if os.Getenv("APP_ENV") == "development" {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// `server reencrypt` seals stored mailbox passwords under the current
	// DESK_SECRET_KEY and exits (key rotation, docs/operations/secrets).
	if len(os.Args) > 1 && os.Args[1] == "reencrypt" {
		if err := app.Reencrypt(ctx, logger); err != nil {
			logger.Fatal().Err(err).Msg("reencrypt failed")
		}
		return
	}
	if err := app.Run(ctx, logger); err != nil {
		logger.Fatal().Err(err).Msg("server failed")
	}
}
