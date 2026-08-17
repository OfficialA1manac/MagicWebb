-- 034_collection_verification.sql
-- The `verified` badge added in migration 012 was admin-curated; the admin
-- surface was deleted in 3d2010d, so nothing writes the column any more.
-- Re-ground the badge in facts the chain and the indexer already know:
--
--   standard_verified  the contract answers ERC-165 supportsInterface() true
--                      for ERC-721 (0x80ac58cd) or ERC-1155 (0xd9b67a26)
--   verified           standard_verified AND its metadata has resolved at
--                      least once (a row exists in nft_metadata)
--
-- Both inputs are stored rather than only the composite so an unbadged
-- collection can be diagnosed without re-running the eth_call.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE collections
  ADD COLUMN IF NOT EXISTS standard_verified BOOLEAN NOT NULL DEFAULT FALSE;

-- NULL = never checked. The sweeper stamps this on every pass, including
-- passes that conclude "not a standard NFT", so a hostile contract cannot be
-- probed on a hot loop.
ALTER TABLE collections
  ADD COLUMN IF NOT EXISTS verification_checked_at TIMESTAMPTZ;

-- The sweeper's claim query is ORDER BY verification_checked_at NULLS FIRST,
-- so never-checked collections sort ahead of stale ones on a single scan.
CREATE INDEX IF NOT EXISTS idx_collections_verification_checked_at
  ON collections(verification_checked_at NULLS FIRST);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_collections_verification_checked_at;
ALTER TABLE collections DROP COLUMN IF EXISTS verification_checked_at;
ALTER TABLE collections DROP COLUMN IF EXISTS standard_verified;
-- +goose StatementEnd
