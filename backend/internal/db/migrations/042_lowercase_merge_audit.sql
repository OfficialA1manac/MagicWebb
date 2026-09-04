-- 042_lowercase_merge_audit.sql
--
-- Safety check for 039_lowercase_addresses.sql. 039 lowercases every address
-- column and, where a row already existed under BOTH casings, keeps the
-- lowercase row and drops the checksummed twin (merging only a handful of
-- collections fields). That is correct for the three Neon databases it has
-- already run on — the duplicates there were empty shells — but it is lossy
-- in general: on a fresh database with populated rows under both casings,
-- the checksummed row's non-key fields are discarded without a merge.
--
-- 039 is append-only history and cannot be changed. This migration runs
-- right after it and FAILS LOUDLY if any table 039 touched still holds two
-- rows that differ only by address case, or any mixed-case address at all,
-- so a rerun against a database where 039 could not complete cleanly aborts
-- instead of the schema silently carrying the loss forward. On a clean
-- database every branch is empty and this is a no-op.
--
-- Idempotent: read-only; it either raises or does nothing, so re-running is
-- harmless. The RAISE names the first offending table so the operator can
-- merge by hand before retrying.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
  bad_table text;
BEGIN
  SELECT t INTO bad_table FROM (
    SELECT 'collections' AS t
     WHERE EXISTS (SELECT 1 FROM collections
                   GROUP BY lower(address) HAVING count(*) > 1)
    UNION ALL
    SELECT 'tracked_collections'
     WHERE EXISTS (SELECT 1 FROM tracked_collections
                   GROUP BY lower(address) HAVING count(*) > 1)
    UNION ALL
    SELECT 'nft_tokens'
     WHERE EXISTS (SELECT 1 FROM nft_tokens
                   GROUP BY lower(collection), token_id HAVING count(*) > 1)
    UNION ALL
    SELECT 'nft_metadata'
     WHERE EXISTS (SELECT 1 FROM nft_metadata
                   GROUP BY lower(collection), token_id HAVING count(*) > 1)
    UNION ALL
    SELECT 'nft_attributes'
     WHERE EXISTS (SELECT 1 FROM nft_attributes
                   GROUP BY lower(collection), token_id, trait_type HAVING count(*) > 1)
    UNION ALL
    SELECT 'trending_scores'
     WHERE EXISTS (SELECT 1 FROM trending_scores
                   GROUP BY lower(collection), "window" HAVING count(*) > 1)
    UNION ALL
    SELECT 'royalties'
     WHERE EXISTS (SELECT 1 FROM royalties
                   GROUP BY lower(collection), token_id HAVING count(*) > 1)
    UNION ALL
    SELECT 'listings'
     WHERE EXISTS (SELECT 1 FROM listings
                   GROUP BY lower(collection), token_id, lower(seller) HAVING count(*) > 1)
    UNION ALL
    SELECT 'nft_ownership'
     WHERE EXISTS (SELECT 1 FROM nft_ownership
                   GROUP BY lower(collection), token_id, lower(owner) HAVING count(*) > 1)
    UNION ALL
    -- Tables 039 only UPDATEs (no composite PK on an address) cannot collide
    -- on lowering, but a lingering mixed-case value means 039 did not run
    -- to completion — refuse to proceed on those too.
    SELECT 'auctions'
     WHERE EXISTS (SELECT 1 FROM auctions
                    WHERE collection <> lower(collection)
                       OR seller <> lower(seller)
                       OR (highest_bidder IS NOT NULL AND highest_bidder <> lower(highest_bidder)))
    UNION ALL
    SELECT 'offers'
     WHERE EXISTS (SELECT 1 FROM offers
                    WHERE collection <> lower(collection) OR bidder <> lower(bidder))
    UNION ALL
    SELECT 'sales'
     WHERE EXISTS (SELECT 1 FROM sales
                    WHERE collection <> lower(collection)
                       OR seller <> lower(seller) OR buyer <> lower(buyer))
    UNION ALL
    SELECT 'bids'
     WHERE EXISTS (SELECT 1 FROM bids WHERE bidder <> lower(bidder))
  ) AS offenders
  LIMIT 1;

  IF bad_table IS NOT NULL THEN
    RAISE EXCEPTION USING
      MESSAGE = format('042_lowercase_merge_audit: table %s still holds address-case duplicates or mixed-case addresses after 039', bad_table),
      HINT    = 'Merge the duplicate rows by hand (039 drops the checksummed twin without merging its fields), then re-run migrations.',
      ERRCODE = 'integrity_constraint_violation';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Read-only check; nothing to undo.
SELECT 1;
-- +goose StatementEnd
