-- 040_listing_chain_cleaned.sql
--
-- Tracks whether a listing's ON-CHAIN struct has been removed, independently
-- of the DB-side `active` flag. The two go out of sync by design: the
-- listing-expiry sweeper flips `active=false` within seconds of expiry (so
-- buyers stop seeing it), but the on-chain Listing struct persists until a
-- KEEPER_ROLE caller runs Marketplace.cleanExpired — and the keeper's
-- clean pass cannot key off `active` without racing the sweeper (it always
-- lost: 1s sweeper tick vs ~60s keeper cadence, so on-chain structs
-- accumulated forever).
--
-- chain_cleaned = TRUE  → the struct is known gone from chain state:
--   * a Sold event consumed it (buy() deletes the struct),
--   * a Cancelled event covered it (seller cancel() or keeper cleanExpired),
--   * or the keeper confirmed a cleanExpired receipt directly.
-- The keeper's sweep selects `NOT chain_cleaned AND expires_at < now()`.
--
-- Backfill: every historical row is marked cleaned. Rows deactivated by sale
-- or cancel genuinely are; the handful deactivated only by DB expiry keep a
-- residual on-chain struct we deliberately skip — retrying them would be
-- indistinguishable from already-cleaned rows (a NotListed revert loop
-- burning keeper gas every pass). Fresh expiries from here on are tracked
-- precisely.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE listings ADD COLUMN IF NOT EXISTS chain_cleaned BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE listings SET chain_cleaned = TRUE WHERE active = FALSE;
-- Partial index for the keeper's sweep: tiny (only not-yet-cleaned rows).
CREATE INDEX IF NOT EXISTS idx_listings_chain_cleanup
    ON listings (expires_at)
 WHERE chain_cleaned = FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_listings_chain_cleanup;
ALTER TABLE listings DROP COLUMN IF EXISTS chain_cleaned;
-- +goose StatementEnd
