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

// SetIfFree stores nonce for address ONLY when no live (unexpired) nonce is
// already in place. Returns true on insert, false when a live nonce for
// this address already exists. This replaces the previous upsert that let
// any caller overwrite a real user's pending SIWE nonce — which was a
// trivial login DoS.
func (s *PgStore) SetIfFree(address, nonce string, ttl time.Duration) (ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), pgOpTimeout)
	defer cancel()
	// A previous nonce for the same address that has EXPIRED but whose row
	// hasn't yet been garbage-collected (cleanup runs every 60s) must NOT
	// block this address from getting a fresh nonce. We bump the row in
	// the conflict branch ONLY when the existing row is already expired,
	// so a still-live nonce still wins (returns false → caller re-issues).
	err := s.pool.QueryRow(ctx,
		`INSERT INTO siwe_nonces(address, nonce, expires_at) VALUES($1,$2,$3)
		 ON CONFLICT(address) DO UPDATE
		   SET nonce=EXCLUDED.nonce, expires_at=EXCLUDED.expires_at
		 WHERE siwe_nonces.expires_at <= now()
		 RETURNING true`,
		address, nonce, time.Now().Add(ttl),
	).Scan(new(bool))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Error().Err(err).Str("address", address).Msg("nonce: pg set-if-free failed")
		}
		return false
	}
	return true
}

// Set upserts the nonce for an address (latest issued nonce wins, per the
// in-memory store's overwrite semantics).
//
// Deprecated: callers that need SIWE issue should use SetIfFree, which
// prevents an attacker from clobbering a real user's pending nonce. Set is
// retained for the in-memory store shim and for tests that explicitly
// exercise the overwrite path.
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
