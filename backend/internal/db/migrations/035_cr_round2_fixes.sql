-- 035_cr_round2_fixes.sql — CodeRabbit round-2 remediation for databases that
-- already ran 001–034. (The source migrations were also corrected for fresh
-- databases; goose never re-runs applied files, so this migration carries the
-- same deltas forward for live deployments.)
--
-- 1. royalties.chain_id — 022 tagged every domain table except royalties.
-- 2. Thumbnail backfill — 033 added thumb_width with no backfill, so
--    pre-existing thumbnail rows (parent_hash set, thumb_width NULL) never
--    match the (parent_hash, thumb_width) lookup and are dead weight. Delete
--    them; the media worker regenerates thumbnails on demand with a width.
-- 3. webhook_configs RLS — the table stores HMAC signing secrets but had no
--    row-level security, unlike api_keys (029). Enable RLS so only the
--    service role reaches it.
-- 4. auth_audit_log retention — the 90-day window is enforced in-process by
--    PgAuditLogger.retentionSweeper (backend/internal/auth/audit.go); this
--    migration performs a one-time catch-up delete for rows that predate it.

-- +goose Up
-- +goose StatementBegin

-- 1. royalties chain tagging (mirrors 022).
ALTER TABLE royalties ADD COLUMN IF NOT EXISTS chain_id BIGINT NOT NULL DEFAULT 0;
DO $$
DECLARE
  dc_chain_id BIGINT;
BEGIN
  SELECT chain_id INTO dc_chain_id FROM deployment_config ORDER BY id DESC LIMIT 1;
  IF dc_chain_id IS NOT NULL AND dc_chain_id > 0 THEN
    UPDATE royalties SET chain_id = dc_chain_id WHERE chain_id = 0;
  END IF;
END $$;

-- 2. Purge unreachable pre-033 thumbnail rows so they regenerate with a width.
DELETE FROM nft_image_blobs WHERE parent_hash IS NOT NULL AND thumb_width IS NULL;

-- 3. RLS on webhook signing secrets (parity with api_keys in 029).
ALTER TABLE webhook_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_delivery_log ENABLE ROW LEVEL SECURITY;

-- 4. One-time retention catch-up (ongoing enforcement lives in Go).
DELETE FROM auth_audit_log WHERE created_at < now() - interval '90 days';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE webhook_delivery_log DISABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_configs DISABLE ROW LEVEL SECURITY;
ALTER TABLE royalties DROP COLUMN IF EXISTS chain_id;
-- +goose StatementEnd
