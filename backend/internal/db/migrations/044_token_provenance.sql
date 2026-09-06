-- 044_token_provenance.sql
--
-- v3.6 wave 3 (owner decision D3, 2026-09-06): the ★ Creator badge shows on
-- an NFT only when its current holder IS the collection's creator AND the
-- token was minted by that creator. "Minted by" = the `to` of the token's
-- first Transfer from the zero address. The indexer records it here from
-- Transfer / TransferSingle / TransferBatch (first mint wins, COALESCE) and
-- cmd/reindexgov -collections backfills it for tokens minted before v3.6.
--
-- The NFT-level ✓ checkmark needs no new column: it is derived at read time
-- from facts every row already carries (collection verified, name present,
-- image served from our store, holder known) — see db/badge.go.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE nft_tokens ADD COLUMN IF NOT EXISTS minter CHAR(42);
CREATE INDEX IF NOT EXISTS nft_tokens_collection_minter_idx ON nft_tokens (collection, minter) WHERE minter IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS nft_tokens_collection_minter_idx;
ALTER TABLE nft_tokens DROP COLUMN IF EXISTS minter;
-- +goose StatementEnd
