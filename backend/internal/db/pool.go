// Package db manages the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog/log"

	// registers "pgx/v5" driver with database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ConnectReadReplica opens a read-only pgxpool for query offloading. Uses
// the same Neon-friendly configuration as Connect(): low connection count,
// aggressive idle timeout, regular health checks. Returns nil when dsn is
// empty (no replica configured) — callers must handle nil pools gracefully
// by falling back to the primary pool for reads.
func ConnectReadReplica(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil // no replica configured
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse read replica config: %w", err)
	}
	// Read replicas typically serve many concurrent queries, so we allow
	// more connections than the primary (but still capped for Neon's free tier).
	cfg.MaxConns = 20
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 4 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect read replica: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping read replica: %w", err)
	}
	log.Info().
		Str("host", cfg.ConnConfig.Host).
		Int32("max_conns", cfg.MaxConns).
		Msg("read replica connected")
	return pool, nil
}

// Connect opens a pgxpool tuned for serverless Postgres (Neon) compatibility.
// Neon terminates idle connections after ~5 minutes, so the pool must:
//   - cap total connections (Neon free tier: 10)
//   - close idle connections before Neon drops them (idle timeout < 5 min)
//   - rotate connections periodically (max lifetime) to avoid stale sockets
//   - health-check regularly so dead connections are detected quickly
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	// Serverless-friendly pool sizing: keep total connections low so
	// the free-tier limit is never exhausted, and don't pre-allocate
	// idle connections (MinConns=0).
	cfg.MaxConns = 10
	cfg.MinConns = 0

	// Close idle connections after 4 minutes — before Neon's ~5-minute
	// server-side idle timeout, so the pool never hands out a connection
	// that the server has already killed.
	cfg.MaxConnIdleTime = 4 * time.Minute

	// Rotate every connection after 30 minutes to avoid lingering
	// sockets that Neon may have transparently replaced.
	cfg.MaxConnLifetime = 30 * time.Minute

	// Health-check every 30s so dead/stale connections are evicted
	// before a query hits them.
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	log.Info().
		Str("host", cfg.ConnConfig.Host).
		Int32("max_conns", cfg.MaxConns).
		Dur("idle_timeout", cfg.MaxConnIdleTime).
		Dur("max_lifetime", cfg.MaxConnLifetime).
		Msg("postgres connected")
	return pool, nil
}

// Migrate runs all pending Goose migrations from the embedded FS.
func Migrate(dsn string) error {
	goose.SetBaseFS(migrations)

	db, err := sql.Open("pgx/v5", dsn)
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
