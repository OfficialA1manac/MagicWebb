package nonce

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

const pgOpTimeout = 3 * time.Second

// PgStore is a Postgres-backed single-use nonce store. Safe across instances:
// GetDel is an atomic DELETE ... RETURNING, so exactly one caller (on any
// instance) can consume a given nonce. Satisfies the Store interface.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPg creates a Postgres-backed store and starts background TTL cleanup.
func NewPg(pool *pgxpool.Pool) *PgStore {
	s := &PgStore{pool: pool}
	go s.cleanup()
	return s
}

// Set upserts the nonce for an address (latest issued nonce wins, per the
// in-memory store's overwrite semantics).
func (s *PgStore) Set(address, nonce string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), pgOpTimeout)
	defer cancel()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO siwe_nonces(address, nonce, expires_at) VALUES($1,$2,$3)
		 ON CONFLICT(address) DO UPDATE SET nonce=EXCLUDED.nonce, expires_at=EXCLUDED.expires_at`,
		address, nonce, time.Now().Add(ttl),
	); err != nil {
		log.Error().Err(err).Str("address", address).Msg("nonce: pg set failed")
	}
}

// GetDel atomically consumes a non-expired nonce. Single-use across instances.
func (s *PgStore) GetDel(address string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), pgOpTimeout)
	defer cancel()
	var v string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM siwe_nonces WHERE address=$1 AND expires_at > now() RETURNING nonce`,
		address,
	).Scan(&v)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Error().Err(err).Str("address", address).Msg("nonce: pg getdel failed")
		}
		return "", false
	}
	return v, true
}

func (s *PgStore) cleanup() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), pgOpTimeout)
		_, _ = s.pool.Exec(ctx, `DELETE FROM siwe_nonces WHERE expires_at <= now()`)
		cancel()
	}
}
