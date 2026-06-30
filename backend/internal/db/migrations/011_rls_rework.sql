-- +goose Up
-- +goose StatementBegin

-- RLS for the 006_rework tables (they shipped without it) + close the 003 gap
-- where public reads were granted TO anon only, locking out the authenticated
-- role under PostgREST. The Go backend connects as a privileged role
-- and is unaffected; these policies govern direct REST/JS access.
--
-- Conventions:
--   * JWT subject is normalized with lower() — the backend stores lowercase
--     addresses, while a wallet-provided `sub` may be EIP-55 checksummed.
--   * Every CREATE POLICY is preceded by DROP POLICY IF EXISTS so a policy
--     applied manually (e.g. via Neon dashboard) never fails goose
--     and blocks startup.
--   * Column-level UPDATE grants keep users from rewriting server-owned
--     columns (notification content, profiles.verified).
--   * Requires the `anon` / `authenticated` roles (as does 003).

-- ── Enable RLS on every 006 table ──────────────────────────────────────────
ALTER TABLE nft_ownership       ENABLE ROW LEVEL SECURITY;
ALTER TABLE tracked_collections ENABLE ROW LEVEL SECURITY;
ALTER TABLE nft_metadata        ENABLE ROW LEVEL SECURITY;
ALTER TABLE nft_attributes      ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications       ENABLE ROW LEVEL SECURITY;
ALTER TABLE profiles            ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports             ENABLE ROW LEVEL SECURITY;

-- ── Public chain-derived data: world-readable, service-only writes ─────────
DROP POLICY IF EXISTS nft_ownership_public_read  ON nft_ownership;
CREATE POLICY nft_ownership_public_read  ON nft_ownership       FOR SELECT TO anon, authenticated USING (true);
DROP POLICY IF EXISTS tracked_public_read        ON tracked_collections;
CREATE POLICY tracked_public_read        ON tracked_collections FOR SELECT TO anon, authenticated USING (true);
DROP POLICY IF EXISTS nft_metadata_public_read   ON nft_metadata;
CREATE POLICY nft_metadata_public_read   ON nft_metadata        FOR SELECT TO anon, authenticated USING (true);
DROP POLICY IF EXISTS nft_attributes_public_read ON nft_attributes;
CREATE POLICY nft_attributes_public_read ON nft_attributes      FOR SELECT TO anon, authenticated USING (true);
DROP POLICY IF EXISTS profiles_public_read       ON profiles;
CREATE POLICY profiles_public_read       ON profiles            FOR SELECT TO anon, authenticated USING (true);

-- ── Notifications: recipient-only; only the read flag is user-writable ─────
DROP POLICY IF EXISTS notifications_self_read ON notifications;
CREATE POLICY notifications_self_read ON notifications FOR SELECT TO authenticated
    USING (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
DROP POLICY IF EXISTS notifications_self_update ON notifications;
CREATE POLICY notifications_self_update ON notifications FOR UPDATE TO authenticated
    USING      (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'))
    WITH CHECK (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
REVOKE UPDATE ON notifications FROM authenticated;
GRANT  UPDATE (read) ON notifications TO authenticated;
GRANT  SELECT ON notifications TO authenticated;

-- ── Profiles: self-managed, but `verified` stays server-owned ──────────────
DROP POLICY IF EXISTS profiles_self_insert ON profiles;
CREATE POLICY profiles_self_insert ON profiles FOR INSERT TO authenticated
    WITH CHECK (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
DROP POLICY IF EXISTS profiles_self_update ON profiles;
CREATE POLICY profiles_self_update ON profiles FOR UPDATE TO authenticated
    USING      (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'))
    WITH CHECK (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
REVOKE UPDATE ON profiles FROM authenticated;
GRANT  UPDATE (display_name, bio, avatar_uri, banner_uri, twitter, website, updated_at)
    ON profiles TO authenticated;
GRANT  SELECT, INSERT ON profiles TO authenticated;
GRANT  SELECT ON profiles TO anon;

-- ── Reports: reporter sees own; authenticated may file as themselves ───────
DROP POLICY IF EXISTS reports_self_read ON reports;
CREATE POLICY reports_self_read ON reports FOR SELECT TO authenticated
    USING (reporter = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
DROP POLICY IF EXISTS reports_self_insert ON reports;
CREATE POLICY reports_self_insert ON reports FOR INSERT TO authenticated
    WITH CHECK (reporter = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));
GRANT SELECT, INSERT ON reports TO authenticated;
GRANT USAGE ON SEQUENCE reports_id_seq TO authenticated;

-- ── Read grants for the public chain data ──────────────────────────────────
GRANT SELECT ON nft_ownership, tracked_collections, nft_metadata, nft_attributes
    TO anon, authenticated;

-- ── 003 gap: extend public reads to the authenticated role ─────────────────
DROP POLICY IF EXISTS collections_auth_read ON collections;
CREATE POLICY collections_auth_read ON collections     FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS nft_tokens_auth_read  ON nft_tokens;
CREATE POLICY nft_tokens_auth_read  ON nft_tokens      FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS listings_auth_read    ON listings;
CREATE POLICY listings_auth_read    ON listings        FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS auctions_auth_read    ON auctions;
CREATE POLICY auctions_auth_read    ON auctions        FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS bids_auth_read        ON bids;
CREATE POLICY bids_auth_read        ON bids            FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS sales_auth_read       ON sales;
CREATE POLICY sales_auth_read       ON sales           FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS royalties_auth_read   ON royalties;
CREATE POLICY royalties_auth_read   ON royalties       FOR SELECT TO authenticated USING (true);
DROP POLICY IF EXISTS trending_auth_read    ON trending_scores;
CREATE POLICY trending_auth_read    ON trending_scores FOR SELECT TO authenticated USING (true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS trending_auth_read    ON trending_scores;
DROP POLICY IF EXISTS royalties_auth_read   ON royalties;
DROP POLICY IF EXISTS sales_auth_read       ON sales;
DROP POLICY IF EXISTS bids_auth_read        ON bids;
DROP POLICY IF EXISTS auctions_auth_read    ON auctions;
DROP POLICY IF EXISTS listings_auth_read    ON listings;
DROP POLICY IF EXISTS nft_tokens_auth_read  ON nft_tokens;
DROP POLICY IF EXISTS collections_auth_read ON collections;

REVOKE ALL ON nft_ownership, tracked_collections, nft_metadata, nft_attributes
    FROM anon, authenticated;
REVOKE ALL ON reports FROM authenticated;
REVOKE USAGE ON SEQUENCE reports_id_seq FROM authenticated;
REVOKE ALL ON profiles FROM anon, authenticated;
REVOKE ALL ON notifications FROM authenticated;

DROP POLICY IF EXISTS reports_self_insert       ON reports;
DROP POLICY IF EXISTS reports_self_read         ON reports;
DROP POLICY IF EXISTS profiles_self_update      ON profiles;
DROP POLICY IF EXISTS profiles_self_insert      ON profiles;
DROP POLICY IF EXISTS notifications_self_update ON notifications;
DROP POLICY IF EXISTS notifications_self_read   ON notifications;
DROP POLICY IF EXISTS profiles_public_read       ON profiles;
DROP POLICY IF EXISTS nft_attributes_public_read ON nft_attributes;
DROP POLICY IF EXISTS nft_metadata_public_read   ON nft_metadata;
DROP POLICY IF EXISTS tracked_public_read        ON tracked_collections;
DROP POLICY IF EXISTS nft_ownership_public_read  ON nft_ownership;

ALTER TABLE reports             DISABLE ROW LEVEL SECURITY;
ALTER TABLE profiles            DISABLE ROW LEVEL SECURITY;
ALTER TABLE notifications       DISABLE ROW LEVEL SECURITY;
ALTER TABLE nft_attributes      DISABLE ROW LEVEL SECURITY;
ALTER TABLE nft_metadata        DISABLE ROW LEVEL SECURITY;
ALTER TABLE tracked_collections DISABLE ROW LEVEL SECURITY;
ALTER TABLE nft_ownership       DISABLE ROW LEVEL SECURITY;

-- +goose StatementEnd
