-- +goose Up
-- +goose StatementBegin

-- 025_event_dedup: prevent duplicate event processing from reorgs or restarts.
-- The indexer's runWatcher may re-process the same event in two scenarios:
--   1. Chain reorg: the indexer rewinds by reorgSafetyBlocks and re-indexes
--      the affected range. Without dedup, handlers dispatch the same event twice
--      (idempotent upserts are safe, but double-dispatch wastes DB writes + SSE
--      broadcasts + notification pushes).
--   2. Crash restart: the persisted indexed_block cursor may be slightly behind
--      the last actually-processed block due to the cursor-advance-at-end
--      pattern in processRange — SetIndexedBlock happens AFTER all handlers
--      fire for a chunk, so a crash mid-chunk re-processes the same block range.
--
-- The unique constraint on (tx_hash, log_index, collection, token_id, event_type)
-- ensures ON CONFLICT DO NOTHING silently skips replayed events without
-- requiring any handler-level dedup logic. The constraint covers the five
-- core marketplace event types tracked in the nft_events summary table
-- (listings, auctions, bids, sales, offers) plus Transfer events (ownership).
--
-- This fixes IDX-3 in the Full Stack Optimization Matrix: "No dedup — same
-- event can be processed twice on reorg."
--
-- Also adds an index on tx_hash for efficient ON CONFLICT lookups and for
-- the activity-feed query pattern (SELECT ... WHERE tx_hash = $1).

-- Step 1: Add index for conflict detection performance.
CREATE INDEX IF NOT EXISTS idx_nft_events_tx_hash ON nft_events(tx_hash);
CREATE INDEX IF NOT EXISTS idx_nft_ownership_changes_tx_hash ON nft_ownership_changes(tx_hash);

-- Step 2: Add unique constraint for idempotent event processing.
-- Covers: nft_events (listings, auctions, bids, sales, offers).
-- Use DO block to handle constraint already existing gracefully.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'nft_events_tx_unique'
          AND conrelid = 'nft_events'::regclass
    ) THEN
        ALTER TABLE nft_events ADD CONSTRAINT nft_events_tx_unique
            UNIQUE (tx_hash, log_index, event_type, collection, token_id);
    END IF;
END $$;

-- Step 3: Add unique constraint for nft_ownership_changes (Transfer events).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'nft_ownership_changes_tx_unique'
          AND conrelid = 'nft_ownership_changes'::regclass
    ) THEN
        ALTER TABLE nft_ownership_changes ADD CONSTRAINT nft_ownership_changes_tx_unique
            UNIQUE (tx_hash, log_index, collection, token_id);
    END IF;
END $$;

-- Step 4: Add unique constraint for activity_feed (the user-facing activity table).
-- Covers the same event types as nft_events but in the activity_feed denormalized form.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'activity_feed_tx_unique'
          AND conrelid = 'activity_feed'::regclass
    ) THEN
        ALTER TABLE activity_feed ADD CONSTRAINT activity_feed_tx_unique
            UNIQUE (tx_hash, log_index, event_type);
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Remove unique constraints added by this migration.
ALTER TABLE IF EXISTS activity_feed DROP CONSTRAINT IF EXISTS activity_feed_tx_unique;
ALTER TABLE IF EXISTS nft_ownership_changes DROP CONSTRAINT IF EXISTS nft_ownership_changes_tx_unique;
ALTER TABLE IF EXISTS nft_events DROP CONSTRAINT IF EXISTS nft_events_tx_unique;

-- Remove indices (optional; keeping them is harmless and they're lightweight).
-- These are dropped in the down migration for cleanliness.
DROP INDEX IF EXISTS idx_nft_ownership_changes_tx_hash;
DROP INDEX IF EXISTS idx_nft_events_tx_hash;

-- +goose StatementEnd
