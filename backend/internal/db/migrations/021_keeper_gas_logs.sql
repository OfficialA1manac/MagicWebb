-- ── Keeper Gas Logs ───────────────────────────────────────────────────────────
-- Tracks gas usage for every keeper transaction (settle, refundLosers,
-- refundExpiredOffer, fee sweep). Enables a gas-cost monitoring dashboard
-- and cost-trend analysis. Each row captures the keeper address, transaction
-- type, hash, gas used, effective gas price, and total cost in wei.
--
-- The effective_gas_cost_wei is computed as gas_used * effective_gas_price
-- from the transaction receipt. This is the actual cost paid by the keeper,
-- not the gas limit.
--
-- See: backend/internal/indexer/runner.go sendRaw() + waitMined() for the
-- logging call site.
-- +goose Up
CREATE TABLE IF NOT EXISTS keeper_gas_logs (
    id BIGSERIAL PRIMARY KEY,
    keeper_addr TEXT NOT NULL,
    tx_type TEXT NOT NULL,       -- settle | refund_losers | refund_offer | fee_sweep
    tx_hash TEXT NOT NULL,
    gas_used BIGINT NOT NULL,
    effective_gas_price_wei NUMERIC NOT NULL,
    effective_gas_cost_wei NUMERIC NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_keeper_gas_logs_created_at ON keeper_gas_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_keeper_gas_logs_tx_type ON keeper_gas_logs(tx_type);

-- +goose Down
DROP TABLE IF EXISTS keeper_gas_logs;
