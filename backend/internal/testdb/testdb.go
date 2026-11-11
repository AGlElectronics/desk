// Package testdb gives tests a migrated, throw-away PostgreSQL schema.
//
// Set DESK_TEST_DATABASE_URL to a disposable database, for example
// postgres://postgres:postgres@localhost:5432/desk_test?sslmode=disable
// (dev only). Without it, database tests skip themselves. Each call creates
// its own schema, so tests and packages may run in parallel.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AGlElectronics/desk/backend/migrations"
)

// EnvVar names the variable with the test database URL.
const EnvVar = "DESK_TEST_DATABASE_URL"

// DemoOrg is the organization the initial migration seeds.
const DemoOrg = "00000000-0000-0000-0000-000000000010"

// New returns a pool whose search_path is a fresh, fully migrated schema.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvVar)
	if url == "" {
		t.Skipf("%s is not set; skipping database test", EnvVar)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect %s: %v", EnvVar, err)
	}
	defer admin.Close(context.Background())
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	schema := "t_" + hex.EncodeToString(buf)
	// Extensions are database-wide; keep them in public so dropping a test
	// schema never drops a type another test uses.
	for _, stmt := range []string{
		`CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public`,
		`CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public`,
		`CREATE SCHEMA ` + schema,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil && !isDuplicate(err) {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = db.Close()

	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, url); err == nil {
			_, _ = c.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
			_ = c.Close(ctx)
		}
	})
	return pool
}

// isDuplicate: two tests creating the same extension at once race on
// pg_extension; the loser's error is harmless.
func isDuplicate(err error) bool {
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "SQLSTATE 42710")
}
