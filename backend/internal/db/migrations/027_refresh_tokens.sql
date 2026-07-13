-- +goose Up
-- +goose StatementBegin

-- 027_refresh_tokens: refresh token family tracking for short-lived access
-- tokens (15min) with rotating 7-day refresh tokens.
--
-- Security model (AUTH-1 from Full Stack Optimization Matrix):
--   - Access tokens: 15min TTL, used for API auth, HttpOnly cookie.
--   - Refresh tokens: 7d TTL, used ONLY at /auth/refresh, rotated on use.
--   - Family-based revocation: each wallet has a refresh_token_family UUID.
--     When a refresh token is used, a new row is inserted with a new token_id
--     and the old one is marked revoked. If a revoked token is reused (replay
--     attack), the ENTIRE family is revoked — all sessions for that wallet
--     are terminated, forcing re-authentication via SIWE.
--
-- Table: refresh_token_families
--   wallet_addr  - the authenticated wallet address
--   family_id    - UUID that ties all refresh tokens for one SIWE session
--   token_id     - unique JWT jti (JWT ID) for this specific refresh token
--   issued_at    - when this token was issued
--   expires_at   - when this token expires
--   revoked      - true when this specific token has been used (rotated)
--   family_revoked - true when the whole family is invalidated (replay detected)

CREATE TABLE IF NOT EXISTS refresh_token_families (
    id             BIGSERIAL PRIMARY KEY,
    wallet_addr     VARCHAR(42) NOT NULL,
    family_id      UUID NOT NULL DEFAULT gen_random_uuid(),
    token_id       VARCHAR(64) NOT NULL UNIQUE,
    issued_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL,
    revoked        BOOLEAN NOT NULL DEFAULT false,
    family_revoked BOOLEAN NOT NULL DEFAULT false
);

-- Index for efficient lookup by wallet + family on refresh.
CREATE INDEX IF NOT EXISTS idx_rtf_wallet_family
    ON refresh_token_families(wallet_addr, family_id);

-- Index for token_id lookup during rotation (the most common query).
CREATE INDEX IF NOT EXISTS idx_rtf_token
    ON refresh_token_families(token_id) WHERE NOT revoked AND NOT family_revoked;

-- Cleanup: periodically remove expired rows (family completely expired).
-- Called by a background goroutine or a cron-triggered DELETE.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS refresh_token_families;

-- +goose StatementEnd
