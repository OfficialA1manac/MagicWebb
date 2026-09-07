-- +goose Up
-- +goose StatementBegin
-- v3.6 wave 6: the image-store health worker evicts unreferenced blobs (LRU)
-- when the store nears MaxTotalBlobBytes. Migration 013 granted the runtime
-- role SELECT/INSERT/UPDATE only; DELETE is needed for eviction. Also index
-- last_seen_at so the LRU scan does not sort the whole table.
DO $$ BEGIN
    EXECUTE format('GRANT DELETE ON nft_image_blobs TO %I', current_user);
END $$;
CREATE INDEX IF NOT EXISTS nft_image_blobs_last_seen_idx ON nft_image_blobs (last_seen_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS nft_image_blobs_last_seen_idx;
DO $$ BEGIN
    EXECUTE format('REVOKE DELETE ON nft_image_blobs FROM %I', current_user);
END $$;
-- +goose StatementEnd
