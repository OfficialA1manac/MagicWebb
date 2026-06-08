-- +goose Up
-- +goose StatementBegin
-- Cumulative effective-bid model: a bidder's EFFECTIVE bid on an auction is the
-- SUM of all their individual bid rows. Winner = max(effective_wei).
-- Each bid tx is its own row (bids.tx_hash is UNIQUE → idempotent indexer inserts).
CREATE INDEX IF NOT EXISTS idx_bids_auction_bidder ON bids (auction_id, bidder);
-- +goose StatementEnd

-- +goose StatementBegin
-- security_invoker so the querying role's RLS on `bids` applies to the view
-- (Postgres 15+; Supabase). bids already grants anon SELECT via 003_rls.sql.
CREATE VIEW effective_bids
    WITH (security_invoker = true) AS
    SELECT auction_id,
           bidder,
           SUM(amount_wei) AS effective_wei,
           COUNT(*)        AS bid_count,
           MIN(placed_at)  AS first_bid_at,
           MAX(placed_at)  AS last_bid_at
    FROM bids
    GROUP BY auction_id, bidder;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT ON effective_bids TO anon, authenticated;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS effective_bids;
DROP INDEX IF EXISTS idx_bids_auction_bidder;
-- +goose StatementEnd
