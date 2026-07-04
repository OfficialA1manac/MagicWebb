-- ── Deployment Configuration ──────────────────────────────────────────────────
-- Tracks which contract addresses were deployed in the most recent deployment.
-- The backend refuses to start if the env vars don't match the stored config,
-- preventing the indexer from mixing events from old and new contract instances.
--
-- On a first deploy (no rows), the backend auto-inserts a row from the current
-- env vars and proceeds. On subsequent starts, every address must match — if
-- any field differs, the operator must TRUNCATE on-chain-derived tables and
-- restart.
--
-- Tables whose data survives a redeploy (users, profiles) are NOT included
-- in the truncate list — only on-chain-derived state gets wiped.
--
-- See: backend/cmd/server/main.go deploymentConfigCheck()
--
-- NOTE: This migration is idempotent. It uses CREATE TABLE IF NOT EXISTS
-- so it can be re-run without error during development.
-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_config (
    id SERIAL PRIMARY KEY,
    chain_id BIGINT NOT NULL,
    marketplace_addr TEXT NOT NULL,
    auction_addr TEXT NOT NULL,
    offerbook_addr TEXT NOT NULL,
    nft_addr TEXT NOT NULL,
    marketplace_manager_addr TEXT NOT NULL DEFAULT '',
    index_from_block BIGINT NOT NULL DEFAULT 0,
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS deployment_config;
