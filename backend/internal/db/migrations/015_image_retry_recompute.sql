-- +goose Up
-- +goose StatementBegin

-- ── Migration 015: image-retry backoff recompute ─────────────────────────────
-- Recompute next_image_retry_at for nft_metadata rows whose values were stamped
-- under the buggy BumpImageRetry XOR schedule, and bring them onto the corrected
-- exponential cadence. Apply AFTER the code-fix deploy that flipped
--   ── BEFORE (buggy) — wait hours = LEAST(count ^ 2, 24)
--                 → count ∈ {1..6}: yields {3, 0, 1, 6, 7, 4} hours
--   ── AFTER  (fixed) — wait hours = LEAST(power(2.0, GREATEST(count-1,0))::int, 24)
--                 → count ∈ {1..6}: yields {1, 2, 4, 8, 16, 24} hours
-- (queries_rework.go:751, BumpImageRetry).
--
-- DEPLOY ORDER  ⚠  DO NOT apply this migration before the code-fix deploy is
-- ─────────────  live across ALL replicas. Running it earlier would compound the
--                bug — we'd be restamping with the same wrong (current production)
--                cadence ourselves. Roll out the code first, then this migration,
--                then standard rollback if either step fails.
--
-- SCOPE  Rows that are actually inside the slow-path retry pipeline, AND have
-- ─────  a buggy-stamped wait window the worker would otherwise honour:
--          image_uri like 'http://%' or 'https://%'  ← still upstream (not yet self-hosted)
--          image_retry_count >= 1                   ← at least one bump happened
--          image_retry_count <  6                   ← not permanently exhausted
--          next_image_retry_at IS NOT NULL          ← a stamp exists to fix
--
-- EFFECT  Each matching row is re-stamped to now() + LEAST(power(...), 24) hours.
-- ──────  This drains the buggy wait windows and brings the token onto the
--         corrected cadence from migration-time onward; the worker's next tick
--         then picks the token up and BumpImageRetry stamps correct values
--         thereafter. Net effect: tokens no longer sit in the old bad retry
--         window waiting out the wrong duration.
--
-- IDEMPOTENCY  Re-running this migration yields values within wall-clock drift
-- ───────────  of the first run — both runs converge to "now + power(...) hours,"
--              so neither correctness nor ordering is sensitive to repeated
--              application. (Goose's own migration-version table prevents
--              accidental double-apply under normal use; the SQL itself is also
--              idempotent for hand-applies during incident response.)

UPDATE nft_metadata
SET next_image_retry_at =
    now()
      + LEAST(power(2.0, GREATEST(image_retry_count - 1, 0))::int, 24)
      * interval '1 hour'
WHERE
    image_uri IS NOT NULL
    AND (image_uri LIKE 'http://%' OR image_uri LIKE 'https://%')
    AND image_retry_count >= 1
    AND image_retry_count <  6
    AND next_image_retry_at IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down is intentionally a no-op. There is no correct "original" state to
-- restore — the buggy wait-time values were unsalvageable by design (the
-- user-facing bug was "wait the wrong duration"), and reapplying them would
-- re-pin tokens to the bad cadence. For a forensic restore path: read
-- nft_metadata, nft_metadata_image_retry_idx from the previous backup and
-- apply only the rows whose next_image_retry_at falls within the buggy
-- cadence set {3, 0, 1, 6, 7, 4} hours ± 1 min of an image_retry_count bump.

SELECT 1;

-- +goose StatementEnd
