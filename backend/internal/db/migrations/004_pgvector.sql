-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS vector;

-- 1536-dim OpenAI ada-002 or 768-dim Cohere embed-v3 (set at deploy time)
ALTER TABLE nft_tokens ADD COLUMN IF NOT EXISTS embedding vector(1536);

-- IVFFlat index for approximate nearest-neighbour search
-- lists = sqrt(expected_rows); tune after initial data load
CREATE INDEX idx_nft_tokens_embedding ON nft_tokens
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Helper function: find similar NFTs by cosine similarity
CREATE OR REPLACE FUNCTION similar_nfts(
    query_embedding vector(1536),
    match_count     INT DEFAULT 20,
    min_similarity  FLOAT DEFAULT 0.7
)
RETURNS TABLE (
    collection  TEXT,
    token_id    NUMERIC,
    similarity  FLOAT
)
LANGUAGE SQL STABLE AS $$
    SELECT
        collection::TEXT,
        token_id,
        1 - (embedding <=> query_embedding) AS similarity
    FROM nft_tokens
    WHERE embedding IS NOT NULL
      AND 1 - (embedding <=> query_embedding) >= min_similarity
    ORDER BY embedding <=> query_embedding
    LIMIT match_count;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS similar_nfts;
DROP INDEX   IF EXISTS idx_nft_tokens_embedding;
ALTER TABLE nft_tokens DROP COLUMN IF EXISTS embedding;
DROP EXTENSION IF EXISTS vector;

-- +goose StatementEnd
