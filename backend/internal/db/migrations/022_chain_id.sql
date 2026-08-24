-- 022_chain_id.sql — Phase 2 multi-chain schema (chain tagging only).
--
-- Adds chain_id BIGINT to every domain table so each per-chain database can
-- tag its rows with the network it indexes (Coston2=114, Songbird=19,
-- Flare=14). MagicWebb runs ONE database per chain, so NO composite-key
-- rework is needed — the existing single-column PKs / UNIQUE constraints
-- stay in place. The app's upserts use ON CONFLICT(address),
-- ON CONFLICT(collection, token_id), ON CONFLICT(collection, token_id, seller),
-- etc., which REQUIRE those original single-column constraints; swapping them
-- to composite (chain_id, …) keys would both break 11 dependent foreign keys
-- (SQLSTATE 2BP01 on a fresh DB) and make every ON CONFLICT upsert fail.
-- chain_id here is purely a filter/label column.
--
-- DEFAULT 0 keeps legacy INSERTs working; the backfill below stamps existing
-- rows with the deployment's configured chain_id read from deployment_config
-- (migration 020). On a first deploy (no deployment_config row yet) the
-- backfill is a no-op and rows keep chain_id = 0 until the indexer writes
-- chain_id explicitly.

-- +goose Up
-- +goose StatementBegin

-- ── chain_id column on every domain table ──────────────────────────────
ALTER TABLE collections         ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nft_tokens          ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nft_ownership       ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nft_metadata        ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nft_attributes      ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tracked_collections ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE listings            ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE auctions            ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE bids                ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE offers              ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sales               ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE trending_scores     ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE keeper_gas_logs     ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE royalties           ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

-- ── Backfill existing rows with the deployment chain_id ─────────────────
DO $$
DECLARE
  dc_chain_id BIGINT;
BEGIN
  SELECT chain_id INTO dc_chain_id FROM deployment_config ORDER BY id DESC LIMIT 1;
  IF dc_chain_id IS NOT NULL AND dc_chain_id > 0 THEN
    UPDATE collections         SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_tokens          SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_ownership       SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_metadata        SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_attributes      SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE tracked_collections SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE listings            SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE auctions            SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE bids                SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE offers              SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE sales               SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE trending_scores     SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE keeper_gas_logs     SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE royalties           SET chain_id = dc_chain_id WHERE chain_id = 0;
  END IF;
END $$;

-- ── Indexes for chain-scoped queries ────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_offers_chain_id       ON offers (chain_id);
CREATE INDEX IF NOT EXISTS idx_listings_chain_active ON listings (chain_id) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_auctions_chain_status ON auctions (chain_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_sales_chain           ON sales (chain_id);

-- +goose StatementEnd
