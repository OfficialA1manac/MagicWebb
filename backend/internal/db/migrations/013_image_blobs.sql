-- +goose Up
-- +goose StatementBegin

-- ── nft_image_blobs ────────────────────────────────────────────────────────
-- Self-hosted, content-addressed image/metadata store. Every blob is keyed
-- by SHA-256 of its bytes, so identical images from different NFT contracts
-- dedupe automatically and we can prove byte-equality without trusting the
-- upstream gateway. The blob is served by the same-origin Go binary at
-- /api/v1/img/<sha256>, so the frontend never touches IPFS / Cloudflare /
-- Pinata — only our PG row is hit at render time. No third-party object
-- storage, no egress fees; capacity is the Supabase free-tier DB bound.
--
-- ── Access model ────────────────────────────────────────────────────────────
-- Direct connection only. The application role (pgxpool) is the sole
-- reader/writer of this table; the Supabase ``anon`` and ``authenticated``
-- roles MUST NOT be able to enumerate our self-hosted blobs. Postgres
-- creates every new table with default privileges for the PUBLIC pseudo-
-- role (so anon / authenticated inherit SELECT through PUBLIC unless we
-- REVOKE). We REVOKE before granting so the only accessor is the project
-- runtime role. We do NOT enable RLS — the Go binary is the real access
-- control boundary, and a permissive ``USING(true)`` policy was documented
-- as "documentation-as-control" (per-row evaluation cost with no actual
-- gating). If this project ever switches to Supabase-auth driven reads,
-- re-evaluate the GRANT and add a restrictive RLS policy THEN, not now.

-- refcount is bumped atomically on every Store() so the same image reused
-- across N tokens is one row + N refcounts, and a future GC job can drop
-- unused rows.
CREATE TABLE IF NOT EXISTS nft_image_blobs (
    sha256       CHAR(64)        PRIMARY KEY,
    mime         TEXT            NOT NULL,
    byte_length  INTEGER         NOT NULL,
    source_uri   TEXT            NOT NULL,
    body         BYTEA           NOT NULL,
    refcount     INTEGER         NOT NULL DEFAULT 1,
    inserted_at  TIMESTAMPTZ     NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ     NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS nft_image_blobs_inserted_idx ON nft_image_blobs (inserted_at);

-- Strip the Postgres PUBLIC default grant so anon / authenticated have NO
-- path to this table. The next statement re-grants to the runtime role only.
REVOKE ALL ON nft_image_blobs FROM PUBLIC;
-- Grant to CURRENT_USER (the role running migrations) instead of hardcoded 'postgres'
-- so this works on Neon, Supabase, and plain Postgres alike.
DO $$ BEGIN
    EXECUTE format('GRANT SELECT, INSERT, UPDATE ON nft_image_blobs TO %I', current_user);
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS nft_image_blobs;
-- +goose StatementEnd
