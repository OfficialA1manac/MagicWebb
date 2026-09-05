// Package auth provides API key verification for machine-to-machine
// authentication (AUTH-3). API keys are HMAC-SHA256 hashed before storage.
//
// Verification only: the marketplace runs with no admin surface, so there is
// no in-process issuance path. Rows in api_keys are provisioned out-of-band
// (directly in the database) if ever needed; this package only verifies them.
//
// Key format: mw_<64-hex-chars> (67 chars total, 32 random bytes).
// Verification: HMAC-SHA256 the incoming key, compare against stored hash.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// APIKeyPrefix is the mandatory prefix for all MagicWebb API keys.
// The full key is APIKeyPrefix + 64 hex chars (32 random bytes).
const APIKeyPrefix = "mw_"

// hashAPIKey computes the HMAC-SHA256 of the API key using a fixed internal
// secret derived from the key prefix. This is NOT a user-configurable salt —
// the HMAC construction prevents length-extension attacks that a bare SHA-256
// would be vulnerable to, and the fixed internal secret ensures cross-instance
// consistency (all instances use the same hashing to verify keys in the shared DB).
func hashAPIKey(key string) []byte {
	mac := hmac.New(sha256.New, []byte("magicwebb-apikey-v1"))
	mac.Write([]byte(key))
	return mac.Sum(nil)
}

// ValidateAPIKeyFormat checks that a key string matches the expected format.
func ValidateAPIKeyFormat(key string) bool {
	if !strings.HasPrefix(key, APIKeyPrefix) {
		return false
	}
	payload := strings.TrimPrefix(key, APIKeyPrefix)
	if len(payload) != 64 {
		return false
	}
	for _, c := range payload {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ── Postgres store ────────────────────────────────────────────────────────

// APIKeyStore verifies API keys. Issuance/revocation happen out-of-band —
// there is no admin surface — so the interface is verification-only.
type APIKeyStore interface {
	// Verify checks a key hash and returns the key metadata on success.
	Verify(ctx context.Context, hash []byte) (*APIKeyInfo, error)
}

// APIKeyInfo is the metadata returned for a verified API key.
type APIKeyInfo struct {
	ID          int64      `json:"id"`
	Label       string     `json:"label"`
	Permissions []string   `json:"permissions"`
	CreatedBy   string     `json:"created_by"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Revoked     bool       `json:"revoked"`
	CreatedAt   time.Time  `json:"created_at"`
}

// PgAPIKeyStore is a Postgres-backed APIKeyStore.
type PgAPIKeyStore struct {
	pool *pgxpool.Pool
}

// NewPgAPIKeyStore creates a Postgres-backed API key store.
func NewPgAPIKeyStore(pool *pgxpool.Pool) *PgAPIKeyStore {
	return &PgAPIKeyStore{pool: pool}
}

// ErrAPIKeyInvalid is returned when a key hash doesn't match any active key.
var ErrAPIKeyInvalid = errors.New("apikey: invalid or revoked key")

func (s *PgAPIKeyStore) Verify(ctx context.Context, hash []byte) (*APIKeyInfo, error) {
	info := &APIKeyInfo{Permissions: []string{}}
	var perms []byte
	err := s.pool.QueryRow(ctx,
		`UPDATE api_keys SET last_used_at = now()
		 WHERE key_hash = $1 AND revoked = false
		   AND (expires_at IS NULL OR expires_at > now())
		 RETURNING id, label, COALESCE(permissions, '[]'::jsonb), created_by, last_used_at, expires_at, revoked, created_at`,
		hash,
	).Scan(&info.ID, &info.Label, &perms, &info.CreatedBy,
		&info.LastUsedAt, &info.ExpiresAt, &info.Revoked, &info.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAPIKeyInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("apikey: verify: %w", err)
	}
	// Parse permissions JSON array using encoding/json.
	if len(perms) > 0 && string(perms) != "[]" {
		_ = json.Unmarshal(perms, &info.Permissions)
	}
	return info, nil
}

// VerifyAndHash is a convenience method: verify a plaintext key and return its info.
// The plaintext key is hashed internally; the caller never handles the hash.
// Returns ErrAPIKeyInvalid when the key is not valid or has been revoked.
func VerifyAndHash(ctx context.Context, store APIKeyStore, plaintext string) (*APIKeyInfo, error) {
	if !ValidateAPIKeyFormat(plaintext) {
		return nil, ErrAPIKeyInvalid
	}
	return store.Verify(ctx, hashAPIKey(plaintext))
}

// ── Audit event types ────────────────────────────────────────────────────

const (
	EventAPIKeyVerified = "apikey_verified"
	EventAPIKeyFailed   = "apikey_failed"
)

// auditDetails marshals audit detail fields with encoding/json. Interpolating
// values into a raw JSON string breaks on quotes/backslashes/control chars,
// and the async audit worker discards insert errors — the row would be lost
// silently on a jsonb column.
func auditDetails(fields map[string]any) string {
	b, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// AuditAPIKeyVerified logs a successful API key verification.
// The IP and UA fields are populated from the request context when available.
func AuditAPIKeyVerified(log AuditLogger, keyID int64, ip, ua, label string) {
	if log == nil {
		return
	}
	log.Log(AuditEntry{
		EventType:  EventAPIKeyVerified,
		WalletAddr: "", // API keys aren't tied to a wallet
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    auditDetails(map[string]any{"key_id": keyID, "label": label}),
	})
}

// AuditAPIKeyFailed logs a failed API key verification.
// The IP and UA fields are populated from the request context when available.
func AuditAPIKeyFailed(log AuditLogger, ip, ua, reason string) {
	if log == nil {
		return
	}
	log.Log(AuditEntry{
		EventType:  EventAPIKeyFailed,
		WalletAddr: "",
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "failure",
		Details:    auditDetails(map[string]any{"reason": reason}),
	})
}
