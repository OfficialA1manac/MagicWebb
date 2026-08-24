-- +goose Up
-- +goose StatementBegin

-- ── Stall-detection indexes ────────────────────────────────────────────────
--
-- GetStalledAuctionCounts and ListStalledAuctions rely on two partial-index
-- patterns to avoid Seq Scans on every admin dashboard load:
--
-- 1. expired_unsettled  →  idx_auctions_active (migration 002)
--    (ends_at) WHERE status='active'
--    Covers: WHERE status='active' AND ends_at < now()
--    Already exists — no change needed.
--
-- 2. inactive_no_bids  →  idx_auctions_inactive_no_bids  (this migration)
--    (starts_at) WHERE status='active' AND highest_bidder IS NULL
--    Covers: WHERE status='active' AND highest_bidder IS NULL
--            AND starts_at < now() - interval '30 minutes'
--    The consumer predicate keeps starts_at bare on the left side so the
--    planner can use this index as a range Index Cond; the arithmetic lives
--    on the now() side. (The inverted form `starts_at + interval < now()`
--    is NOT indexable against a bare starts_at key.)

CREATE INDEX IF NOT EXISTS idx_auctions_inactive_no_bids
    ON auctions (starts_at)
    WHERE status = 'active' AND highest_bidder IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_auctions_inactive_no_bids;

-- +goose StatementEnd
