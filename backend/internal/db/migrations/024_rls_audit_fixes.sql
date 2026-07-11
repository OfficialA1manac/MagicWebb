-- +goose Up
-- +goose StatementBegin

-- RLS audit fixes (2025-07-11). Cross-referenced all RLS policies in 003 and 011
-- against the full table inventory and the API surface (rest.go, queries.go,
-- queries_rework.go). The Go backend connects as a privileged role that
-- bypasses RLS entirely, so none of these affect the live site. They close
-- defence-in-depth gaps that matter if PostgREST or a dashboard ever exposes
-- direct DB access.
--
-- Fixes applied:
--   1. Profiles INSERT column-level grant — excludes `verified` so a user
--      cannot self-verify via a direct INSERT (HIGH).
--   2. Case-sensitivity mismatch in 003 policies — wraps `sub` extraction
--      in lower() to match the DB's lowercase storage (MEDIUM).
--   3. Offers `offers_auth_read` — bridges the anon-only gap that 011 fixed
--      for every other table but missed for offers (MEDIUM).
--   3a. Offers `offers_owner_read` — lets NFT owners see all offers (any
--      status) on tokens they currently hold via PostgREST. The API already
--      exposes this via ListOffers' Owner filter (MEDIUM).
--   4. RLS on six tables: saved_searches (new), pending_withdrawals
--      (re-enable, 012 already has RLS+self-read), keeper_gas_logs (new),
--      gas_alert_history (new), deployment_config (new, was the last table
--      without any RLS), nft_image_blobs (new with public SELECT).
--   5. effective_bids view already ships with security_invoker=true (007)
--      — no change needed. Confirmed during audit.

-- ── 1. Profiles INSERT: prevent self-verification ───────────────────────────
-- 011 correctly REVOKE's all-then-GRANT's column-level UPDATE, but INSERT was
-- left as a blanket GRANT. Revoke the broad INSERT and re-grant only the
-- user-writable columns.

REVOKE INSERT ON profiles FROM authenticated;
GRANT INSERT (address, display_name, bio, avatar_uri, banner_uri, twitter, website, updated_at)
    ON profiles TO authenticated;

-- ── 2. Case-sensitivity: wrap JWT sub in lower() for 003 policies ───────────
-- 011 uses lower() everywhere; 003 predates that convention. Wallet addresses
-- in JWTs can be EIP-55 checksummed (mixed-case) while the DB stores lowercase.
-- Fix the three 003 policies that missed the lower() wrap.

DROP POLICY IF EXISTS users_self_read ON users;
CREATE POLICY users_self_read ON users FOR SELECT TO authenticated
    USING (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS users_self_update ON users;
CREATE POLICY users_self_update ON users FOR UPDATE TO authenticated
    USING (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'))
    WITH CHECK (address = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS offers_self_read ON offers;
CREATE POLICY offers_self_read ON offers FOR SELECT TO authenticated
    USING (bidder = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

-- ── 3. Offers: bridge anon → authenticated read gap ────────────────────────
-- 003 grants offers_public_read TO anon only. 011 bridged this gap for every
-- other table (collections, nft_tokens, listings, auctions, bids, sales,
-- royalties, trending_scores) but missed offers. Authenticated PostgREST users
-- must be able to see non-expired pending offers just like anonymous users.

DROP POLICY IF EXISTS offers_auth_read ON offers;
CREATE POLICY offers_auth_read ON offers FOR SELECT TO authenticated
    USING (status = 'pending' AND expires_at > now());

-- 3a. offers_owner_read: NFT owners can see all offers (any status) on tokens
--     they currently hold. Mirrors the ListOffers Owner filter in the Go API.
--     Joins nft_ownership to verify current ownership. Unlike offers_auth_read
--     (pending-only) and offers_self_read (bidder-only), this reveals the full
--     offer history for a token the authenticated user owns.

DROP POLICY IF EXISTS offers_owner_read ON offers;
CREATE POLICY offers_owner_read ON offers FOR SELECT TO authenticated
    USING (EXISTS (
        SELECT 1 FROM nft_ownership n
        WHERE n.collection = offers.collection
          AND n.token_id = offers.token_id
          AND lower(n.owner) = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub')
          AND n.units > 0
    ));

-- ── 4. Enable RLS + policies on five unprotected tables ────────────────────

-- 4a. saved_searches — self-CRUD (the API already gates this behind JWT auth)
ALTER TABLE saved_searches ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS saved_searches_self_read ON saved_searches;
CREATE POLICY saved_searches_self_read ON saved_searches FOR SELECT TO authenticated
    USING (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS saved_searches_self_insert ON saved_searches;
CREATE POLICY saved_searches_self_insert ON saved_searches FOR INSERT TO authenticated
    WITH CHECK (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

DROP POLICY IF EXISTS saved_searches_self_delete ON saved_searches;
CREATE POLICY saved_searches_self_delete ON saved_searches FOR DELETE TO authenticated
    USING (user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub'));

GRANT SELECT, INSERT, DELETE ON saved_searches TO authenticated;
GRANT USAGE ON SEQUENCE saved_searches_id_seq TO authenticated;

-- 4b. pending_withdrawals — already has RLS + self-read policy from 012.
--     Re-enabling here as a belt-and-braces idempotent guard; the existing
--     pending_withdrawals_self_read policy remains in place.
ALTER TABLE pending_withdrawals ENABLE ROW LEVEL SECURITY;

-- 4c. keeper_gas_logs — service_role only (admin dashboard).
ALTER TABLE keeper_gas_logs ENABLE ROW LEVEL SECURITY;

-- 4d. gas_alert_history — service_role only (admin dashboard).
ALTER TABLE gas_alert_history ENABLE ROW LEVEL SECURITY;

-- 4e. deployment_config — service_role only. Stores deployment metadata
--     (chain ID, contract addresses) that the backend uses to verify it's
--     talking to the correct deployed contracts. Contract addresses are
--     public on-chain but deployment metadata should not be writable by
--     unprivileged roles.
ALTER TABLE deployment_config ENABLE ROW LEVEL SECURITY;

-- 4f. nft_image_blobs — public SELECT (images served via /api/v1/img/:sha256);
--     service_role for INSERT (writes happen during metadata ingest).
ALTER TABLE nft_image_blobs ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS nft_image_blobs_public_read ON nft_image_blobs;
CREATE POLICY nft_image_blobs_public_read ON nft_image_blobs FOR SELECT TO anon, authenticated
    USING (true);

GRANT SELECT ON nft_image_blobs TO anon, authenticated;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 4f: back out nft_image_blobs
REVOKE SELECT ON nft_image_blobs FROM anon, authenticated;
DROP POLICY IF EXISTS nft_image_blobs_public_read ON nft_image_blobs;
ALTER TABLE nft_image_blobs DISABLE ROW LEVEL SECURITY;

-- 4e: back out deployment_config
ALTER TABLE deployment_config DISABLE ROW LEVEL SECURITY;

-- 4d: back out gas_alert_history
ALTER TABLE gas_alert_history DISABLE ROW LEVEL SECURITY;

-- 4c: back out keeper_gas_logs
ALTER TABLE keeper_gas_logs DISABLE ROW LEVEL SECURITY;

-- 4b: back out pending_withdrawals
ALTER TABLE pending_withdrawals DISABLE ROW LEVEL SECURITY;

-- 4a: back out saved_searches
REVOKE USAGE ON SEQUENCE saved_searches_id_seq FROM authenticated;
REVOKE SELECT, INSERT, DELETE ON saved_searches FROM authenticated;
DROP POLICY IF EXISTS saved_searches_self_delete ON saved_searches;
DROP POLICY IF EXISTS saved_searches_self_insert ON saved_searches;
DROP POLICY IF EXISTS saved_searches_self_read ON saved_searches;
ALTER TABLE saved_searches DISABLE ROW LEVEL SECURITY;

-- 3a: back out offers_owner_read
DROP POLICY IF EXISTS offers_owner_read ON offers;

-- 3: back out offers_auth_read
DROP POLICY IF EXISTS offers_auth_read ON offers;
-- Re-create the original (without lower(), matching 003 shape)
DROP POLICY IF EXISTS offers_self_read ON offers;
CREATE POLICY offers_self_read ON offers FOR SELECT TO authenticated
    USING (bidder = current_setting('request.jwt.claims', true)::jsonb->>'sub');

-- 2: back out case-sensitivity fix — restore original 003 policies without lower().
--     offers_self_read was already restored by section 3 above; only users policies
--     need reversal here.
DROP POLICY IF EXISTS users_self_update ON users;
CREATE POLICY users_self_update ON users FOR UPDATE TO authenticated
    USING (address = current_setting('request.jwt.claims', true)::jsonb->>'sub')
    WITH CHECK (address = current_setting('request.jwt.claims', true)::jsonb->>'sub');

DROP POLICY IF EXISTS users_self_read ON users;
CREATE POLICY users_self_read ON users FOR SELECT TO authenticated
    USING (address = current_setting('request.jwt.claims', true)::jsonb->>'sub');

-- 1: back out profiles INSERT column-level grant — restore broad INSERT
REVOKE INSERT ON profiles FROM authenticated;
GRANT INSERT ON profiles TO authenticated;

-- +goose StatementEnd
