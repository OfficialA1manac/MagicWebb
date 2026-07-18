-- 033_thumbnail_width.sql
-- IMG-1: Add thumb_width column so the /api/v1/img/:sha256?size=128 handler
-- can look up thumbnails by (parent_hash, width) without decoding image bytes.
-- NULL for full-size images, set to the target pixel width for thumbnails.

ALTER TABLE nft_image_blobs
  ADD COLUMN IF NOT EXISTS thumb_width INTEGER;

-- Speed up the ?size= lookup: WHERE parent_hash = $1 AND thumb_width = $2 LIMIT 1.
-- The parent_hash index (idx_nft_image_blobs_parent_hash) already exists from
-- migration 026, but it doesn't include thumb_width. Add a composite index
-- so the thumbnail lookup is a single index scan rather than a filter after
-- an index scan on parent_hash alone.
CREATE INDEX IF NOT EXISTS idx_nft_image_blobs_parent_width
  ON nft_image_blobs(parent_hash, thumb_width)
  WHERE parent_hash IS NOT NULL AND thumb_width IS NOT NULL;
