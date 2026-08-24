-- 030_indexer_checkpoints.sql
-- IDX-2: Per-collection indexer checkpoints for crash-resilient restart.
--
-- The global indexer_state table stores the last scanned block for all
-- marketplace events (listings, auctions, bids, sales). However, the
-- Transfer event scanning (processTransfers) runs separately per collection
-- and a restart re-scans from the deploy block even when the global cursor
-- is far ahead — making cold-start recovery O(N_collections × full history).
--
-- This migration adds a per-collection checkpoint so each tracked collection
-- resumes indexing from its last-scanned Transfer block. Combined with
-- parallel scanning (IDX-1), crash recovery is O(1) instead of O(full history).

-- Add last_scanned_block to tracked_collections if not already present.
-- Some deployments may have this column from an earlier migration; the
-- DO block makes this idempotent.
-- +goose Up
-- +goose StatementBegin
-- ADD COLUMN IF NOT EXISTS resolves the table through search_path directly.
-- (The previous information_schema probe filtered on table_name only — a
-- same-named table in ANY visible schema could skip the ALTER and break the
-- index/COMMENT statements below.)
-- `tracked` and `updated_at` are required by the indexer checkpoint queries
-- (GetCollectionCheckpoints / GetMinCollectionCheckpoint filter WHERE
-- tracked = true; SetCollectionCheckpoint writes updated_at). Default
-- tracked = true means every enrolled collection is swept unless soft-disabled.
ALTER TABLE tracked_collections
  ADD COLUMN IF NOT EXISTS last_scanned_block BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_scanned_hash  BYTEA,
  ADD COLUMN IF NOT EXISTS tracked            BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS updated_at         TIMESTAMPTZ NOT NULL DEFAULT now();

-- Index for the sweeper query that picks collections needing catch-up.
-- The indexer's transfer scan reads tracked_collections ordered by
-- last_scanned_block ASC so collections furthest behind are scanned first.
CREATE INDEX IF NOT EXISTS idx_tracked_collections_scanned_block
    ON tracked_collections (last_scanned_block ASC)
    WHERE tracked = true;

COMMENT ON COLUMN tracked_collections.last_scanned_block IS
'Last block number whose Transfer events were fully indexed for this collection. Advances after each successful processTransfers chunk.';
COMMENT ON COLUMN tracked_collections.last_scanned_hash IS
'Block hash of last_scanned_block for reorg detection. NULL after initial insert — set on first successful scan.';
-- +goose StatementEnd
