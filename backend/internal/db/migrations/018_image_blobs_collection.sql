-- +goose Up
-- +goose StatementBegin

-- Add collection column to track which NFT contract triggered each blob's first
-- insert. Used by imagestore.Put() to enforce MaxBlobCountPerCollection (1,000
-- unique blobs per collection). The column is set on INSERT only; ON CONFLICT
-- (dedup) does NOT update it, so each row's collection reflects the first
-- requester — subsequent collections that reference the same bytes via dedup
-- do not count against their own per-collection quota.
--
-- Existing rows get an empty string, which is treated as "unattributed" — they
-- do not count toward any collection's quota, and Put() from the indexer sets
-- it to the actual collection address on new inserts.
ALTER TABLE nft_image_blobs ADD COLUMN IF NOT EXISTS collection TEXT NOT NULL DEFAULT '';

-- Index for CountBlobsForCollection queries. A B-tree on collection covers the
-- COUNT/DISTINCT query pattern efficiently since most collections store far
-- fewer than 1,000 blobs and the index is small.
CREATE INDEX IF NOT EXISTS nft_image_blobs_collection_idx ON nft_image_blobs (collection);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS nft_image_blobs_collection_idx;
ALTER TABLE nft_image_blobs DROP COLUMN IF EXISTS collection;
-- +goose StatementEnd
