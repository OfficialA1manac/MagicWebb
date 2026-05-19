// Package db manages the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog/log"

	// goose postgres driver
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

// Connect opens a pgxpool with default settings and runs Goose migrations.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(sanitizeDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
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

	db, err := goose.OpenDBWithDriver("pgx", sanitizeDSN(dsn))
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
