-- +goose Up
-- +goose StatementBegin

ALTER TABLE nft_metadata
    ADD COLUMN image_retry_count  INT          NOT NULL DEFAULT 0,
    ADD COLUMN next_image_retry_at TIMESTAMPTZ;

CREATE INDEX nft_metadata_image_retry_idx
    ON nft_metadata (next_image_retry_at)
    WHERE image_uri LIKE 'http%';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS nft_metadata_image_retry_idx;
ALTER TABLE nft_metadata
    DROP COLUMN IF EXISTS next_image_retry_at,
    DROP COLUMN IF EXISTS image_retry_count;

-- +goose StatementEnd
