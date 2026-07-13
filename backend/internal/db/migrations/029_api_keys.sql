-- +goose Up
-- +goose StatementBegin

-- 029_api_keys: HMAC API keys for machine-to-machine authentication.
-- API keys are issued to admin scripts, external monitoring, and indexer
-- callbacks so they don't need to share user JWT cookies. The plaintext
-- key is returned ONCE at creation time and stored as an HMAC-SHA256 hash.
--
-- Key format: mw_<64-hex-chars> (67 chars total, 32 random bytes).
-- The prefix "mw_" distinguishes API keys from other bearer tokens.
--
-- Labels are human-readable names (e.g. "Prometheus scraper", "CI pipeline").
-- Permissions are a JSON array of allowed actions (e.g. ["metrics:read"]).
-- When empty/null, the key has full read access (backward-compatible default).

CREATE TABLE api_keys (
    id          SERIAL PRIMARY KEY,
    label       TEXT NOT NULL,
    key_hash    BYTEA NOT NULL UNIQUE,          -- HMAC-SHA256 of the plaintext key
    permissions JSONB DEFAULT '[]'::jsonb,       -- ["metrics:read", "admin:write", ...]
    created_by  CHAR(42) NOT NULL,               -- admin wallet address that created this key
    last_used_at TIMESTAMPTZ,                    -- updated on every successful verification
    expires_at  TIMESTAMPTZ,                     -- NULL = never expires
    revoked     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for fast revocation lookups and key hash verification.
CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_created_by ON api_keys(created_by);

-- RLS: only the service role can read/write api_keys.
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS api_keys;

-- +goose StatementEnd
