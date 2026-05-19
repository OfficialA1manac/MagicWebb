-- +goose Up
-- +goose StatementBegin

ALTER TABLE nft_tokens ADD COLUMN IF NOT EXISTS search_vec tsvector
    GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(name, '') || ' ' || coalesce(description, ''))
    ) STORED;

ALTER TABLE collections ADD COLUMN IF NOT EXISTS search_vec tsvector
    GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(name, '') || ' ' || coalesce(symbol, ''))
    ) STORED;

CREATE INDEX idx_nft_tokens_search  ON nft_tokens  USING GIN (search_vec);
CREATE INDEX idx_collections_search ON collections USING GIN (search_vec);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_collections_search;
DROP INDEX IF EXISTS idx_nft_tokens_search;
ALTER TABLE collections DROP COLUMN IF EXISTS search_vec;
ALTER TABLE nft_tokens  DROP COLUMN IF EXISTS search_vec;

-- +goose StatementEnd
