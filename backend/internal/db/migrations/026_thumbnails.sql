-- +goose Up
-- +goose StatementBegin

-- 026_thumbnails: add thumbnail variant support to the image blob store.
-- Thumbnail variants (128px, 256px, 512px width) are stored as separate rows
-- in nft_image_blobs linked to their parent full-size image via parent_hash.
-- This enables the frontend to request appropriately-sized images for listing
-- cards (48 per page at up to 500KB each vs 48 thumbnails at ~5KB each — a
-- 100x bandwidth reduction on listing pages).
--
-- Storage model:
--   parent_hash IS NULL     → full-size original image (existing rows)
--   parent_hash IS NOT NULL → thumbnail variant linked to its parent
--
-- Quota enforcement: thumbnail rows do NOT count toward per-collection blob
-- quotas (CountBlobsForCollection excludes rows where parent_hash IS NOT NULL)
-- because they derive from already-counted full-size originals. Total byte
-- quota still applies since thumbnails consume real storage.

ALTER TABLE nft_image_blobs ADD COLUMN IF NOT EXISTS parent_hash VARCHAR(64);

-- Index for thumbnail lookups: find all variants for a given parent hash.
CREATE INDEX IF NOT EXISTS idx_nft_image_blobs_parent_hash
    ON nft_image_blobs(parent_hash) WHERE parent_hash IS NOT NULL;

-- Update the per-collection count function used by quota enforcement.
-- Thumbnail rows (parent_hash IS NOT NULL) do NOT count toward the
-- MaxBlobCountPerCollection ceiling since they derive from already-counted
-- full-size originals.
-- (The Go code in queries.go uses WHERE parent_hash IS NULL for this count.)

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_nft_image_blobs_parent_hash;
ALTER TABLE nft_image_blobs DROP COLUMN IF EXISTS parent_hash;

-- +goose StatementEnd
