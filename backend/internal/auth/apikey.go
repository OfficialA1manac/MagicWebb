// Package auth provides API key generation and verification for
// machine-to-machine authentication (AUTH-3). API keys are HMAC-SHA256
// hashed before storage — the plaintext key is returned once at creation
// and cannot be retrieved afterwards.
//
// Key format: mw_<64-hex-chars> (67 chars total, 32 random bytes).
// Verification: HMAC-SHA256 the incoming key, compare against stored hash.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// APIKeyBytes is the number of random bytes in an API key payload.
const APIKeyBytes = 32

// ── Key generation ────────────────────────────────────────────────────────

// GenerateAPIKey creates a new API key with the format "mw_<64-hex-chars>".
// Returns both the plaintext key (to show to the user once) and the HMAC-SHA256
// hash (to store in the database). The plaintext key MUST NOT be logged or
// persisted — it's returned to the caller exactly once at creation time.
func GenerateAPIKey() (plaintext string, hash []byte, err error) {
	var b [APIKeyBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("apikey: rand read: %w", err)
	}
	plaintext = APIKeyPrefix + hex.EncodeToString(b[:])
	hash = hashAPIKey(plaintext)
	return plaintext, hash, nil
}

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

// APIKeyStore persists and verifies API keys.
type APIKeyStore interface {
	// Create inserts a new API key and returns its ID.
	Create(ctx context.Context, label, createdBy string, permissions []string, hash []byte, expiresAt *time.Time) (int64, error)
	// Verify checks a key hash and returns the key metadata on success.
	Verify(ctx context.Context, hash []byte) (*APIKeyInfo, error)
	// Revoke marks a key as revoked by ID.
	Revoke(ctx context.Context, id int64, revokedBy string) error
	// List returns all API keys for an admin.
	List(ctx context.Context, createdBy string) ([]APIKeyInfo, error)
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

func (s *PgAPIKeyStore) Create(ctx context.Context, label, createdBy string, permissions []string, hash []byte, expiresAt *time.Time) (int64, error) {
	permsBytes, _ := json.Marshal(permissions)
	perms := string(permsBytes)
	if perms == "" || perms == "null" {
		perms = "[]"
	}

	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO api_keys(label, key_hash, permissions, created_by, expires_at)
		 VALUES($1, $2, $3::jsonb, $4, $5)
		 RETURNING id`,
		label, hash, perms, createdBy, expiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("apikey: create: %w", err)
	}
	return id, nil
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

func (s *PgAPIKeyStore) Revoke(ctx context.Context, id int64, revokedBy string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET revoked = true WHERE id = $1 AND created_by = $2`,
		id, revokedBy,
	)
	if err != nil {
		return fmt.Errorf("apikey: revoke: %w", err)
	}
	return nil
}

func (s *PgAPIKeyStore) List(ctx context.Context, createdBy string) ([]APIKeyInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, label, COALESCE(permissions, '[]'::jsonb), created_by, last_used_at, expires_at, revoked, created_at
		 FROM api_keys WHERE created_by = $1 ORDER BY created_at DESC`,
		createdBy,
	)
	if err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	defer rows.Close()

	var out []APIKeyInfo
	for rows.Next() {
		var info APIKeyInfo
		var perms []byte
		if err := rows.Scan(&info.ID, &info.Label, &perms, &info.CreatedBy,
			&info.LastUsedAt, &info.ExpiresAt, &info.Revoked, &info.CreatedAt); err != nil {
			return nil, fmt.Errorf("apikey: scan: %w", err)
		}
		if len(perms) > 0 && string(perms) != "[]" {
			_ = json.Unmarshal(perms, &info.Permissions)
		}
		out = append(out, info)
	}
	if out == nil {
		out = []APIKeyInfo{}
	}
	return out, rows.Err()
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
	EventAPIKeyCreated  = "apikey_created"
	EventAPIKeyRevoked  = "apikey_revoked"
	EventAPIKeyVerified = "apikey_verified"
	EventAPIKeyFailed   = "apikey_failed"
)

// AuditAPIKeyCreated logs API key creation.
func AuditAPIKeyCreated(log AuditLogger, adminAddr, ip, ua, label string) {
	if log == nil {
		return
	}
	log.Log(AuditEntry{
		EventType:  EventAPIKeyCreated,
		WalletAddr: adminAddr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    fmt.Sprintf(`{"label":"%s"}`, label),
	})
}

// AuditAPIKeyRevoked logs API key revocation.
func AuditAPIKeyRevoked(log AuditLogger, adminAddr, ip, ua string, keyID int64) {
	if log == nil {
		return
	}
	log.Log(AuditEntry{
		EventType:  EventAPIKeyRevoked,
		WalletAddr: adminAddr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    fmt.Sprintf(`{"key_id":%d}`, keyID),
	})
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
		Details:    fmt.Sprintf(`{"key_id":%d,"label":"%s"}`, keyID, label),
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
		Details:    fmt.Sprintf(`{"reason":"%s"}`, reason),
	})
}
