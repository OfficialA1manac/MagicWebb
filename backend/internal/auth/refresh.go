package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RefreshStore provides refresh token family persistence for rotation.
// Implemented by a Postgres-backed store using the refresh_token_families table.
type RefreshStore interface {
	// IssueRefreshFamily creates a new family for a wallet (new SIWE session).
	// Returns the family_id and the first token_id.
	IssueRefreshFamily(ctx context.Context, walletAddr string, ttl time.Duration) (familyID, tokenID string, err error)

	// RotateRefreshToken atomically revokes the old token_id and issues
	// a new one within the same family. Returns an error if the family
	// has been revoked (replay attack detected) or the old token was
	// already used.
	RotateRefreshToken(ctx context.Context, familyID, oldTokenID string, ttl time.Duration) (newTokenID string, err error)

	// RevokeFamily invalidates all refresh tokens for a family (logout).
	RevokeFamily(ctx context.Context, walletAddr, familyID string) error
}

// PgRefreshStore is a Postgres-backed RefreshStore.
type PgRefreshStore struct {
	pool *pgxpool.Pool
}

// NewPgRefreshStore creates a Postgres-backed refresh token store.
func NewPgRefreshStore(pool *pgxpool.Pool) *PgRefreshStore {
	s := &PgRefreshStore{pool: pool}
	go s.cleanup()
	return s
}

func (s *PgRefreshStore) IssueRefreshFamily(ctx context.Context, walletAddr string, ttl time.Duration) (familyID, tokenID string, err error) {
	familyID = uuid.New().String()
	tokenID = newTokenID()

	_, err = s.pool.Exec(ctx,
		`INSERT INTO refresh_token_families(wallet_addr, family_id, token_id, expires_at)
		 VALUES($1, $2, $3, $4)`,
		walletAddr, familyID, tokenID, time.Now().Add(ttl),
	)
	if err != nil {
		return "", "", fmt.Errorf("refresh: issue family: %w", err)
	}
	return familyID, tokenID, nil
}

func (s *PgRefreshStore) RotateRefreshToken(ctx context.Context, familyID, oldTokenID string, ttl time.Duration) (nextID string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("refresh: rotate begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Lock the family row to serialise concurrent rotations and prevent
	//    false-positive replay detection. Without FOR UPDATE, two concurrent
	//    requests with the same oldTokenID race: the first revokes the token + inserts,
	//    the second sees revoked=true and incorrectly flags a replay attack,
	//    revoking the entire family and booting the legitimate user.
	var familyRevoked bool
	err = tx.QueryRow(ctx,
		`SELECT bool_or(family_revoked) FROM refresh_token_families
		 WHERE family_id=$1 FOR UPDATE`,
		familyID,
	).Scan(&familyRevoked)
	if err != nil && err != pgx.ErrNoRows {
		return "", fmt.Errorf("refresh: family lock: %w", err)
	}
	if familyRevoked {
		return "", fmt.Errorf("refresh: family revoked — re-authentication required")
	}

	// 2. Check the old token: must exist, not revoked, not expired.
	//    The FOR UPDATE above serialises access so only one caller can
	//    evaluate this row at a time.
	var revoked bool
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT revoked, expires_at FROM refresh_token_families
		 WHERE token_id=$1 AND family_id=$2 FOR UPDATE`,
		oldTokenID, familyID,
	).Scan(&revoked, &expiresAt)
	if err == pgx.ErrNoRows {
		return "", fmt.Errorf("refresh: unknown token")
	}
	if err != nil {
		return "", fmt.Errorf("refresh: token check: %w", err)
	}
	if revoked {
		// REPLAY ATTACK DETECTED: the FOR UPDATE lock guarantees no race
		// condition — this token was genuinely reused by a different request.
		// Revoke the entire family so the attacker (who has the old refresh
		// token) can't refresh again. The legitimate user must re-authenticate.
		_, _ = tx.Exec(ctx,
			`UPDATE refresh_token_families SET family_revoked=true WHERE family_id=$1`,
			familyID,
		)
		_ = tx.Commit(ctx)
		return "", fmt.Errorf("refresh: token replay detected — family revoked")
	}
	if time.Now().After(expiresAt) {
		_, _ = tx.Exec(ctx,
			`UPDATE refresh_token_families SET revoked=true WHERE token_id=$1`,
			oldTokenID,
		)
		_ = tx.Commit(ctx)
		return "", fmt.Errorf("refresh: token expired")
	}

	// 3. Revoke the old token and issue a new one (atomic rotation).
	nextID = newTokenID()
	expiry := time.Now().Add(ttl)
	_, err = tx.Exec(ctx,
		`UPDATE refresh_token_families SET revoked=true WHERE token_id=$1;
		 INSERT INTO refresh_token_families(wallet_addr, family_id, token_id, expires_at)
		 SELECT wallet_addr, family_id, $2, $3
		 FROM refresh_token_families
		 WHERE token_id=$1
		 LIMIT 1`,
		oldTokenID, nextID, expiry,
	)
	if err != nil {
		return "", fmt.Errorf("refresh: rotate insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("refresh: rotate commit: %w", err)
	}
	return nextID, nil
}

func (s *PgRefreshStore) RevokeFamily(ctx context.Context, walletAddr, familyID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_token_families SET revoked=true, family_revoked=true
		 WHERE wallet_addr=$1 AND family_id=$2`,
		walletAddr, familyID,
	)
	return err
}

func (s *PgRefreshStore) cleanup() {
	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = s.pool.Exec(ctx,
			`DELETE FROM refresh_token_families WHERE expires_at < now() - interval '1 hour'`)
		cancel()
	}
}

// newTokenID generates a cryptographically random 32-byte hex token ID.
func newTokenID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback to UUID on crypto/rand failure (extremely unlikely).
		return uuid.New().String()
	}
	return hex.EncodeToString(b[:])
}
