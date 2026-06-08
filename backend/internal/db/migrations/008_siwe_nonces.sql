-- +goose Up
-- +goose StatementBegin
-- Multi-instance SIWE nonce store. A nonce issued by one server instance must
-- be consumable by any other, so it cannot live in per-process memory.
CREATE TABLE siwe_nonces (
    address    CHAR(42)    PRIMARY KEY,
    nonce      TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_siwe_nonces_expires ON siwe_nonces (expires_at);
-- Service-role only: RLS enabled with no client policies, so anon/authenticated
-- cannot read or forge nonces; the backend connects with the service role.
ALTER TABLE siwe_nonces ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS siwe_nonces;
-- +goose StatementEnd
