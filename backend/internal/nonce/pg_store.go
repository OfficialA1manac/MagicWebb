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
	// Race-safe nonce issuance.
	//
	// The previous ON CONFLICT DO UPDATE WHERE expires_at <= now() pattern did
	// NOT eliminate the race: a concurrent SetIfFree from another caller could
	// read the same expired row, both pass the WHERE evaluation at slightly
	// different snapshots, and BOTH callers would see their own row (each
	// considering the row "watermark" stale at the moment of their own index
	// scan). Both would return true → both treat their nonce as live.
	//
	// New approach: chain DELETE (any expired row) + INSERT ON CONFLICT DO
	// NOTHING in a single transaction with row-level locking. Sequence:
	//   1. DELETE any expired row for this address (locks it).
	//   2. INSERT the new nonce with ON CONFLICT DO NOTHING RETURNING.
	//      - If the row was just deleted by step 1, INSERT succeeds.
	//      - If a concurrent transaction is holding the row lock, this
	//        INSERT blocks until that txn commits; it then either sees
	//        a fresh row (expires_at > now()) and returns nothing, or
	//        sees a freshly-deleted row and succeeds.
	//   3. RETURNING evaluates to true iff the row was just inserted.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error().Err(err).Str("address", address).Msg("nonce: pg set-if-free begin failed")
		return false
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM siwe_nonces WHERE address=$1 AND expires_at <= now()`,
		address,
	); err != nil {
		log.Error().Err(err).Str("address", address).Msg("nonce: pg set-if-free cleanup failed")
		return false
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO siwe_nonces(address, nonce, expires_at) VALUES($1,$2,$3)
		 ON CONFLICT(address) DO NOTHING
		 RETURNING true`,
		address, nonce, time.Now().Add(ttl),
	).Scan(new(bool))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Error().Err(err).Str("address", address).Msg("nonce: pg set-if-free insert failed")
		}
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		log.Error().Err(err).Str("address", address).Msg("nonce: pg set-if-free commit failed")
		return false
	}
	return true
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
