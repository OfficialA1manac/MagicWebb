-- +goose Up
-- +goose StatementBegin

-- ── pending_withdrawals ─────────────────────────────────────────────────────
-- "Withdraw required" tracking (review IN-03). LoserRefunded / RefundPushed
-- fire whether the ETH push landed or fell back to pendingReturns, so events
-- only SEED candidate rows; the withdrawal sweeper verifies each address
-- against AuctionHouse.pendingReturns() on-chain and either deletes the row
-- (push landed / user withdrew — withdrawals emit no event) or marks it
-- verified with the owed amount so the UI can surface a withdraw button.
CREATE TABLE pending_withdrawals (
    address     CHAR(42)        PRIMARY KEY,
    amount_wei  NUMERIC(78,0)   NOT NULL DEFAULT 0,
    verified    BOOLEAN         NOT NULL DEFAULT FALSE,
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);

ALTER TABLE pending_withdrawals ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS pending_withdrawals_self_read ON pending_withdrawals;
CREATE POLICY pending_withdrawals_self_read ON pending_withdrawals FOR SELECT TO authenticated
    USING (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
GRANT SELECT ON pending_withdrawals TO authenticated;

-- ── verified collections ────────────────────────────────────────────────────
-- Curation flag surfaced as a badge on listing cards and collection pages.
-- Set via the admin API (allowlisted SIWE wallets) or service role.
ALTER TABLE collections ADD COLUMN verified BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS verified;
DROP POLICY IF EXISTS pending_withdrawals_self_read ON pending_withdrawals;
DROP TABLE IF EXISTS pending_withdrawals;
-- +goose StatementEnd
