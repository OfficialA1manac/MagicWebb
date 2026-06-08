// Package db manages the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog/log"

	// registers "pgx/v5" driver with database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

// sanitizeDSN strips pgx-unknown query params (e.g. Supabase pool_mode) that
// cause pgxpool.ParseConfig and the pgx stdlib adapter to reject the DSN.
func sanitizeDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Del("pool_mode")
	u.RawQuery = q.Encode()
	return u.String()
}

// migrationDSN prepares a DSN for goose migrations: strips pool_mode and
// switches Supabase transaction pooler port 6543 → 5432 (session pooler)
// so DDL statements in migrations are not rejected.
func migrationDSN(dsn string) string {
	return strings.ReplaceAll(sanitizeDSN(dsn), ":6543/", ":5432/")
}

// SessionDSN returns a session-mode DSN (Supabase pooler 6543 → 5432, pgx-unknown
// params stripped). Required for LISTEN/NOTIFY, which the transaction-mode pooler
// does not support.
func SessionDSN(dsn string) string { return migrationDSN(dsn) }

// Connect opens a pgxpool with default settings and runs Goose migrations.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(sanitizeDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	// Supabase transaction-mode pooler (port 6543) rotates the backing server
	// connection per transaction, so server-side prepared statements cached by
	// pgx fail with "prepared statement \"...\" does not exist". Simple protocol
	// sends each query inline with no prepared-statement cache → pooler-safe.
	if cfg.ConnConfig.Port == 6543 {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		log.Info().Msg("pgx: simple query protocol (transaction-mode pooler on :6543)")
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	log.Info().Str("host", cfg.ConnConfig.Host).Msg("postgres connected")
	return pool, nil
}

// Migrate runs all pending Goose migrations from the embedded FS.
func Migrate(dsn string) error {
	goose.SetBaseFS(migrations)

	db, err := sql.Open("pgx/v5", migrationDSN(dsn))
	if err != nil {
		return fmt.Errorf("db: open for migration: %w", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	log.Info().Msg("db migrations up to date")
	return nil
}
