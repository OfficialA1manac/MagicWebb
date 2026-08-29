-- 037_saved_searches_lower_idx.sql — index the RLS lookup expression.
--
-- The saved_searches RLS policies (016) filter with lower(user_addr), but
-- saved_searches_user_idx was built on the raw user_addr column, so Postgres
-- cannot use it as a selective equality lookup for the policy predicate and
-- per-user reads degrade to scans as the table grows. Rebuild the index on
-- the expression the policies actually use. (016 was also corrected for
-- fresh databases; goose never re-runs applied files, so this migration
-- carries the same delta forward for live deployments.)

-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS saved_searches_user_idx;
CREATE INDEX IF NOT EXISTS saved_searches_user_idx
    ON saved_searches (lower(user_addr), created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS saved_searches_user_idx;
CREATE INDEX IF NOT EXISTS saved_searches_user_idx
    ON saved_searches (user_addr, created_at DESC);
-- +goose StatementEnd
