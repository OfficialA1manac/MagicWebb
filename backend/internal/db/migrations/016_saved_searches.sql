-- +goose Up
-- +goose StatementBegin

-- ── saved_searches ─────────────────────────────────────────────────────────
-- Persists user-defined filter presets keyed by wallet address. Each row holds
-- a human-readable name, the page context (listings/auctions), and the URL
-- params that encode the filter state (collection, traits, price range, sort).
-- Users can save/load/delete their searches. The index supports efficient
-- listing per user ordered by creation date (newest first).
CREATE TABLE saved_searches (
    id          BIGSERIAL     PRIMARY KEY,
    user_addr   CHAR(42)      NOT NULL,
    name        TEXT          NOT NULL,
    page        TEXT          NOT NULL CHECK (page IN ('listings','auctions')),
    params      TEXT          NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX saved_searches_user_idx ON saved_searches (user_addr, created_at DESC);

-- RLS: users may only read/write their own rows.
ALTER TABLE saved_searches ENABLE ROW LEVEL SECURITY;

-- RLS policies use the Supabase JWT convention (same as 011_rls_rework).
-- The Go backend connects as a privileged role and is unaffected.
DROP POLICY IF EXISTS saved_searches_self_select ON saved_searches;
CREATE POLICY saved_searches_self_select ON saved_searches FOR SELECT TO authenticated
    USING (lower(user_addr) = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS saved_searches_self_insert ON saved_searches;
CREATE POLICY saved_searches_self_insert ON saved_searches FOR INSERT TO authenticated
    WITH CHECK (lower(user_addr) = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS saved_searches_self_delete ON saved_searches;
CREATE POLICY saved_searches_self_delete ON saved_searches FOR DELETE TO authenticated
    USING (lower(user_addr) = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

GRANT SELECT, INSERT, DELETE ON saved_searches TO authenticated;
GRANT USAGE ON SEQUENCE saved_searches_id_seq TO authenticated;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS saved_searches;

-- +goose StatementEnd
