-- +goose Up
-- +goose StatementBegin

-- Create roles that exist in Supabase but may not exist in plain Postgres (Neon).
-- Using DO blocks so migration doesn't fail when roles already exist.
DO $$ BEGIN CREATE ROLE anon; EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE ROLE authenticated; EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ── Enable RLS on every table ──────────────────────────────────────────────
ALTER TABLE collections     ENABLE ROW LEVEL SECURITY;
ALTER TABLE nft_tokens      ENABLE ROW LEVEL SECURITY;
ALTER TABLE listings        ENABLE ROW LEVEL SECURITY;
ALTER TABLE auctions        ENABLE ROW LEVEL SECURITY;
ALTER TABLE bids            ENABLE ROW LEVEL SECURITY;
ALTER TABLE offers          ENABLE ROW LEVEL SECURITY;
ALTER TABLE users           ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales           ENABLE ROW LEVEL SECURITY;
ALTER TABLE royalties       ENABLE ROW LEVEL SECURITY;
ALTER TABLE trending_scores ENABLE ROW LEVEL SECURITY;
ALTER TABLE indexer_state   ENABLE ROW LEVEL SECURITY;

-- ── Public read (anon role) ────────────────────────────────────────────────
CREATE POLICY collections_public_read     ON collections     FOR SELECT TO anon USING (true);
CREATE POLICY nft_tokens_public_read      ON nft_tokens      FOR SELECT TO anon USING (true);
CREATE POLICY listings_public_read        ON listings        FOR SELECT TO anon USING (true);
CREATE POLICY auctions_public_read        ON auctions        FOR SELECT TO anon USING (true);
CREATE POLICY bids_public_read            ON bids            FOR SELECT TO anon USING (true);
CREATE POLICY sales_public_read           ON sales           FOR SELECT TO anon USING (true);
CREATE POLICY royalties_public_read       ON royalties       FOR SELECT TO anon USING (true);
CREATE POLICY trending_public_read        ON trending_scores FOR SELECT TO anon USING (true);

-- ── Offers: public read of non-expired pending; bidder/owner full read ─────
CREATE POLICY offers_public_read ON offers FOR SELECT TO anon
    USING (status = 'pending' AND expires_at > now());

-- authenticated users see their own offers regardless of status
CREATE POLICY offers_self_read ON offers FOR SELECT TO authenticated
    USING (
        bidder = current_setting('request.jwt.claims', true)::jsonb->>'sub'
        -- The owner-side check is enforced at application layer via SIWE session
    );

-- ── Users: own row only ────────────────────────────────────────────────────
CREATE POLICY users_self_read ON users FOR SELECT TO authenticated
    USING (address = current_setting('request.jwt.claims', true)::jsonb->>'sub');

CREATE POLICY users_self_update ON users FOR UPDATE TO authenticated
    USING (address = current_setting('request.jwt.claims', true)::jsonb->>'sub')
    WITH CHECK (address = current_setting('request.jwt.claims', true)::jsonb->>'sub');

-- ── Service role bypasses all policies ────────────────────────────────────
-- (service_role is the Supabase server-side role; RLS is bypassed automatically)

-- ── indexer_state: backend service only ───────────────────────────────────
-- No anon/authenticated policy → only service_role can read/write

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS users_self_update     ON users;
DROP POLICY IF EXISTS users_self_read       ON users;
DROP POLICY IF EXISTS offers_self_read      ON offers;
DROP POLICY IF EXISTS offers_public_read    ON offers;
DROP POLICY IF EXISTS trending_public_read  ON trending_scores;
DROP POLICY IF EXISTS royalties_public_read ON royalties;
DROP POLICY IF EXISTS sales_public_read     ON sales;
DROP POLICY IF EXISTS bids_public_read      ON bids;
DROP POLICY IF EXISTS auctions_public_read  ON auctions;
DROP POLICY IF EXISTS listings_public_read  ON listings;
DROP POLICY IF EXISTS nft_tokens_public_read ON nft_tokens;
DROP POLICY IF EXISTS collections_public_read ON collections;

ALTER TABLE indexer_state   DISABLE ROW LEVEL SECURITY;
ALTER TABLE trending_scores DISABLE ROW LEVEL SECURITY;
ALTER TABLE royalties       DISABLE ROW LEVEL SECURITY;
ALTER TABLE sales           DISABLE ROW LEVEL SECURITY;
ALTER TABLE users           DISABLE ROW LEVEL SECURITY;
ALTER TABLE offers          DISABLE ROW LEVEL SECURITY;
ALTER TABLE bids            DISABLE ROW LEVEL SECURITY;
ALTER TABLE auctions        DISABLE ROW LEVEL SECURITY;
ALTER TABLE listings        DISABLE ROW LEVEL SECURITY;
ALTER TABLE nft_tokens      DISABLE ROW LEVEL SECURITY;
ALTER TABLE collections     DISABLE ROW LEVEL SECURITY;

-- +goose StatementEnd
