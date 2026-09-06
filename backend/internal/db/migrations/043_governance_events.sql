-- 043_governance_events.sql
--
-- v3.6 wave 1: the admin/keeper/upgrade lifecycle becomes visible.
--
-- Until now the indexer filtered only the three trading cores, so every
-- MarketplaceManager event (KeeperSet, AdminTransferStarted/Cancelled/
-- Transferred, AdminRenounced) and every core upgrade event (UpgradeQueued,
-- UpgradeCancelled, ERC-1967 Upgraded) went straight into the void. The audit
-- report calls UpgradeQueued "the highest-signal security event in the system"
-- — this table is where it lands, and /api/v1/governance + /status read it.
--
-- Append-only history keyed by (tx_hash, log_index) so a re-index (cmd/reindexgov)
-- is idempotent. Numbering note: 031 was never written; 042 is the previous head.
--
-- profiles.security_alerts: owner decision 2026-09-06 (7.3) — every signed-in
-- user receives an in-app notification for these events by default and can opt
-- out from their profile.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS governance_events (
    id            BIGSERIAL    PRIMARY KEY,
    chain_id      BIGINT       NOT NULL,
    block_number  BIGINT       NOT NULL,
    tx_hash       CHAR(66)     NOT NULL,
    log_index     INTEGER      NOT NULL,
    contract      CHAR(42)     NOT NULL,   -- emitting address (manager or a core proxy)
    event         TEXT         NOT NULL,   -- KeeperSet | AdminTransferStarted | ... | UpgradeQueued | Upgraded
    actor         CHAR(42),                -- msg.sender / previous holder when the event carries one
    subject       CHAR(42),                -- new keeper / pending admin / implementation
    extra         TEXT,                    -- eta, notes
    block_time    TIMESTAMPTZ  NOT NULL,
    UNIQUE (tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS governance_events_chain_block_idx
    ON governance_events (chain_id, block_number DESC, log_index DESC);

ALTER TABLE profiles
  ADD COLUMN IF NOT EXISTS security_alerts BOOLEAN NOT NULL DEFAULT true;
GRANT UPDATE (security_alerts) ON profiles TO authenticated;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE profiles DROP COLUMN IF EXISTS security_alerts;
DROP TABLE IF EXISTS governance_events;
-- +goose StatementEnd
