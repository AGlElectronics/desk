// Package db wraps the pgx connection pool and provides a transaction
// helper. The pool is created once at startup and shared across the entire
// request lifecycle.
//
// Note: this package deliberately does NOT expose a MigrateUp function.
// Migrations run via cmd/migrate as a controlled one-shot (Compose service
// or K8s Job). See docs/adr/0003-migrate-job.md for the rationale.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a new pool and verifies connectivity with a bounded ping.
// The DSN comes from DATABASE_URL in config; we parse it so pgx-level knobs
// (max conns, timeouts) can be set regardless of query-string overrides.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, errors.New("empty database DSN")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}
	// Keep the pool conservative in the scaffold; raise per-deployment once
	// real workload numbers exist (techstack.md §3 performance budgets).
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// WithTx runs fn inside a database transaction. If fn returns nil the tx
// commits; otherwise it rolls back. The deferred Rollback is safe to run on
// an already-committed tx — pgx reports this as ErrTxClosed and we discard.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
