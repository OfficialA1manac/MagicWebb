-- +goose Up
-- +goose StatementBegin

-- ── Expression index for listings price queries ────────────────────────────
--
-- The existing idx_listings_price index on listings(price_wei) is a TEXT
-- column index — it cannot be used for ORDER BY or WHERE filters that
-- CAST(price_wei AS NUMERIC) for numeric comparison/sorting. ListActiveListings
-- and ListAuctions both use CAST(... AS NUMERIC) for price-range filters and
-- price-based sorts, which forces a full sequential scan + in-memory sort.
--
-- This expression index on CAST(price_wei AS NUMERIC) allows Postgres to
-- satisfy price-based ORDER BY and inequality filters from the index alone,
-- avoiding Seq Scan + Sort on every listings query with price filters.
-- The `WHERE active = true` partial predicate matches the common query path
-- so the index stays small and fast.
--
-- Goose serialises migrations within a transaction, so CONCURRENTLY is
-- unnecessary (no concurrent DML can conflict) and would fail with
-- "cannot run inside a transaction block". A plain CREATE INDEX is safe.
CREATE INDEX IF NOT EXISTS idx_listings_price_numeric
    ON listings (CAST(price_wei AS NUMERIC))
    WHERE active = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_listings_price_numeric;

-- +goose StatementEnd
