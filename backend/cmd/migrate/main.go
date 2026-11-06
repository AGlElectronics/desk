// Package main is Desk's dedicated Goose migration binary.
//
// It is intentionally separate from cmd/server:
//
//   - The API container MUST NOT run migrations on every start — multiple
//     replicas would race, and auto-migrate is forbidden by the ecosystem
//     tech stack policy (techstack.md §4).
//   - Docker Compose runs this as a one-shot `migrate` service that must
//     complete successfully before the backend starts.
//   - Kubernetes runs this as a one-shot Job (expand/migrate/contract) that
//     fires before a Deployment rollout.
//
// Usage:
//
//	migrate up        apply all pending migrations (default)
//	migrate status    print current migration status
//	migrate version   print current migration version
//	migrate down      roll back the latest migration (DEV ONLY; never
//	                  trust `down` to recover deleted data — restore from
//	                  backup instead)
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/migrations"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	zerolog.TimeFieldFormat = time.RFC3339
	if os.Getenv("APP_ENV") == "development" {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	_ = godotenv.Load()

	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "PostgreSQL DSN (defaults to $DATABASE_URL)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logger.Fatal().Err(err).Msg("parse flags")
	}

	cmd := "up"
	if args := fs.Args(); len(args) > 0 {
		cmd = args[0]
	}

	if *dsn == "" {
		logger.Fatal().Msg("DATABASE_URL is required (or pass -dsn)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx, logger, *dsn, cmd); err != nil {
		logger.Fatal().Err(err).Str("command", cmd).Msg("migrate failed")
	}
}

func run(ctx context.Context, logger zerolog.Logger, dsn, cmd string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Fail fast if the DB is unreachable rather than waiting for the first
	// migration statement to time out.
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	switch cmd {
	case "up":
		logger.Info().Msg("applying pending migrations")
		if err := goose.UpContext(ctx, db, "."); err != nil {
			return fmt.Errorf("goose up: %w", err)
		}
		logger.Info().Msg("migrations applied")
	case "status":
		if err := goose.StatusContext(ctx, db, "."); err != nil {
			return fmt.Errorf("goose status: %w", err)
		}
	case "version":
		if err := goose.VersionContext(ctx, db, "."); err != nil {
			return fmt.Errorf("goose version: %w", err)
		}
	case "down":
		logger.Warn().Msg("rolling back the latest migration (DEV ONLY)")
		if err := goose.DownContext(ctx, db, "."); err != nil {
			return fmt.Errorf("goose down: %w", err)
		}
	default:
		return errors.New("unknown command: " + cmd + " (use: up | status | version | down)")
	}
	return nil
}
