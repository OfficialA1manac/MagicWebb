package db

// Webhook HMAC signing secrets need the raw value at delivery time, so they
// cannot be hashed like api_keys. Instead they are encrypted at rest with
// AES-256-GCM using a key held OUTSIDE the database (WEBHOOK_ENC_KEY env var,
// 64 hex chars = 32 bytes). A leaked backup, query log, or compromised read
// replica then discloses only ciphertext.
//
// Storage format: "enc:v1:" + hex(nonce || ciphertext).
//
// Rollout is migration-free: rows written before the key was configured stay
// plaintext and decrypt as-is; every upsert re-encrypts. If WEBHOOK_ENC_KEY
// is unset, secrets are stored plaintext (previous behavior) and a warning is
// logged once at first use. If WEBHOOK_ENC_KEY is SET but malformed, the
// operator's intent was encryption — that is a hard startup error (surfaced
// via ValidateWebhookEncKey from Connect), never a silent plaintext downgrade.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

const webhookSecretPrefix = "enc:v1:"

var (
	webhookEncOnce sync.Once
	webhookEncAEAD cipher.AEAD // nil when no key configured or key invalid
	webhookEncErr  error       // non-nil when a key was configured but unusable
)

func initWebhookAEAD() {
	raw := strings.TrimSpace(os.Getenv("WEBHOOK_ENC_KEY"))
	if raw == "" {
		log.Warn().Msg("db: WEBHOOK_ENC_KEY unset — webhook signing secrets stored plaintext at rest")
		return
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		webhookEncErr = fmt.Errorf("db: WEBHOOK_ENC_KEY set but invalid — must be 64 hex chars (32 bytes)")
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		webhookEncErr = fmt.Errorf("db: webhook secret cipher init failed: %w", err)
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		webhookEncErr = fmt.Errorf("db: webhook secret GCM init failed: %w", err)
		return
	}
	webhookEncAEAD = aead
}

// ValidateWebhookEncKey distinguishes "no key configured" (nil — plaintext
// storage, previous behavior) from "key configured but invalid" (error).
// Connect calls this so a malformed WEBHOOK_ENC_KEY fails the deployment at
// startup instead of silently downgrading secret storage to plaintext.
func ValidateWebhookEncKey() error {
	webhookEncOnce.Do(initWebhookAEAD)
	return webhookEncErr
}

func webhookAEAD() cipher.AEAD {
	webhookEncOnce.Do(initWebhookAEAD)
	return webhookEncAEAD
}

// encryptWebhookSecret returns the at-rest representation of a secret.
// Plaintext passthrough when no key is configured.
func encryptWebhookSecret(secret string) string {
	aead := webhookAEAD()
	if aead == nil || secret == "" {
		if webhookEncErr != nil && secret != "" {
			// Startup validation should have made this unreachable; log
			// loudly if a caller bypassed Connect.
			log.Error().Err(webhookEncErr).Msg("db: invalid WEBHOOK_ENC_KEY — storing webhook secret plaintext")
		}
		return secret
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		// Losing encryption is preferable to losing the webhook: store
		// plaintext (previous behavior) rather than failing the request.
		log.Error().Err(err).Msg("db: webhook secret nonce generation failed — storing plaintext")
		return secret
	}
	ct := aead.Seal(nil, nonce, []byte(secret), nil)
	return webhookSecretPrefix + hex.EncodeToString(append(nonce, ct...))
}

// decryptWebhookSecret reverses encryptWebhookSecret. Legacy plaintext rows
// (no prefix) are returned unchanged. A row that was encrypted but cannot be
// decrypted (key rotated away, key unset, corrupt blob) returns an error
// rather than "": the caller must NOT fall back to an unsigned delivery,
// because a receiver that only checks for header presence would keep trusting
// an endpoint whose signature it can no longer verify. Callers drop the
// config instead, so the delivery visibly fails.
func decryptWebhookSecret(stored string) (string, error) {
	if !strings.HasPrefix(stored, webhookSecretPrefix) {
		return stored, nil
	}
	aead := webhookAEAD()
	if aead == nil {
		return "", fmt.Errorf("db: encrypted webhook secret found but WEBHOOK_ENC_KEY unset or invalid")
	}
	blob, err := hex.DecodeString(strings.TrimPrefix(stored, webhookSecretPrefix))
	if err != nil || len(blob) < aead.NonceSize() {
		return "", fmt.Errorf("db: malformed encrypted webhook secret")
	}
	pt, err := aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("db: webhook secret decrypt failed (rotated key?)")
	}
	return string(pt), nil
}
