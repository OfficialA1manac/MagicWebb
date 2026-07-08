-- 022_chain_id.sql — Phase 2 multi-chain schema.
-- Adds chain_id BIGINT to all domain tables so a single database can hold
-- data for multiple chains (Coston2=114, Songbird=19, Flare=14).
--
-- DEFAULT 0 ensures existing INSERTs without chain_id don't break — the
-- indexer writes chain_id explicitly, and the default catches any legacy
-- code paths. Existing rows get chain_id = 0 initially; deployments should
-- UPDATE rows to their configured CHAIN_ID after migration.
--
-- All columns are NOT NULL with DEFAULT 0. NOT NULL prevents silent NULL
-- contamination; DEFAULT 0 provides backward compatibility for Phase 2
-- (single-indexer, single-chain-per-deployment).

-- ── Core marketplace tables ─────────────────────────────────────────────

ALTER TABLE collections
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE nft_tokens
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE nft_ownership
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE nft_metadata
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE nft_attributes
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE tracked_collections
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE listings
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE auctions
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE bids
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE offers
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE sales
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE trending_scores
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE keeper_gas_logs
  ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;

-- ── Unique constraint updates for multi-chain ──────────────────────────
-- Collections must be unique PER CHAIN (same address on different chains
-- maps to different contracts). Drop single-column unique/pk, recreate as
-- composite with chain_id.

-- collections: address was the PK; now (chain_id, address) is the PK so
-- the same contract address can exist on different chains. Dropping the
-- address-only PK is required — a UNIQUE(chain_id, address) alongside a
-- PK(address) would still reject duplicate addresses across chains.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'collections_pkey') THEN
    ALTER TABLE collections DROP CONSTRAINT collections_pkey;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'collections_address_key') THEN
    ALTER TABLE collections DROP CONSTRAINT collections_address_key;
  END IF;
END $$;
ALTER TABLE collections
  ADD PRIMARY KEY (chain_id, address);

-- nft_tokens: PK is (collection, token_id). Make it (chain_id, collection, token_id).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'nft_tokens_pkey') THEN
    ALTER TABLE nft_tokens DROP CONSTRAINT nft_tokens_pkey;
  END IF;
END $$;
ALTER TABLE nft_tokens
  ADD PRIMARY KEY (chain_id, collection, token_id);

-- nft_ownership: unique on (collection, token_id, owner)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'nft_ownership_collection_token_id_owner_key') THEN
    ALTER TABLE nft_ownership DROP CONSTRAINT nft_ownership_collection_token_id_owner_key;
  END IF;
END $$;
ALTER TABLE nft_ownership
  ADD CONSTRAINT nft_ownership_chain_coll_tok_owner_key UNIQUE (chain_id, collection, token_id, owner);

-- nft_metadata: unique on (collection, token_id)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'nft_metadata_collection_token_id_key') THEN
    ALTER TABLE nft_metadata DROP CONSTRAINT nft_metadata_collection_token_id_key;
  END IF;
END $$;
ALTER TABLE nft_metadata
  ADD CONSTRAINT nft_metadata_chain_coll_tok_key UNIQUE (chain_id, collection, token_id);

-- nft_attributes: composite unique
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'nft_attributes_collection_token_id_trait_type_key') THEN
    ALTER TABLE nft_attributes DROP CONSTRAINT nft_attributes_collection_token_id_trait_type_key;
  END IF;
END $$;
ALTER TABLE nft_attributes
  ADD CONSTRAINT nft_attributes_chain_coll_tok_trait_key UNIQUE (chain_id, collection, token_id, trait_type);

-- tracked_collections: unique on (collection)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'tracked_collections_collection_key') THEN
    ALTER TABLE tracked_collections DROP CONSTRAINT tracked_collections_collection_key;
  END IF;
END $$;
ALTER TABLE tracked_collections
  ADD CONSTRAINT tracked_collections_chain_coll_key UNIQUE (chain_id, collection);

-- listings: PK is (collection, token_id, seller)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'listings_pkey') THEN
    ALTER TABLE listings DROP CONSTRAINT listings_pkey;
  END IF;
END $$;
ALTER TABLE listings
  ADD PRIMARY KEY (chain_id, collection, token_id, seller);

-- offers: PK is offer_id (UUID). Add chain_id index only — offer_id
-- is globally unique (UUID v4), so the PK stays on offer_id.
CREATE INDEX IF NOT EXISTS idx_offers_chain_id ON offers (chain_id);

-- trending_scores: PK was (collection, window)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'trending_scores_pkey') THEN
    ALTER TABLE trending_scores DROP CONSTRAINT trending_scores_pkey;
  END IF;
END $$;
ALTER TABLE trending_scores
  ADD PRIMARY KEY (chain_id, collection, "window");

-- ── Backfill existing rows with deployment chain_id ─────────────────
-- All existing rows got chain_id=0 from the DEFAULT above. Without a
-- backfill, chain-scoped endpoints (which filter on config.C.ChainID, e.g.
-- 114) would return zero results for all pre-migration data. We read the
-- chain_id from deployment_config (migration 020) and stamp every domain
-- table. On first deploy (no deployment_config rows yet) this is a no-op.
DO $$
DECLARE
  dc_chain_id BIGINT;
BEGIN
  SELECT chain_id INTO dc_chain_id FROM deployment_config ORDER BY id DESC LIMIT 1;
  IF dc_chain_id IS NOT NULL AND dc_chain_id > 0 THEN
    UPDATE collections        SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_tokens         SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_ownership      SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_metadata       SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE nft_attributes     SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE tracked_collections SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE listings           SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE auctions           SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE bids               SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE offers             SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE sales              SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE trending_scores    SET chain_id = dc_chain_id WHERE chain_id = 0;
    UPDATE keeper_gas_logs    SET chain_id = dc_chain_id WHERE chain_id = 0;
  END IF;
END $$;

-- ── Index for chain-scoped queries ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_listings_chain_active ON listings (chain_id) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_auctions_chain_status ON auctions (chain_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_sales_chain ON sales (chain_id);
