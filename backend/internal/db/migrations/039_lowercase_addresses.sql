-- 039_lowercase_addresses.sql
--
-- Backfill: the chain indexer wrote EIP-55 CHECKSUMMED addresses, every reader
-- lowercases before querying, and the address columns are case-sensitive
-- CHAR(42). Rows written before the addrStr() fix (internal/indexer/handlers.go)
-- are therefore invisible to every per-address and per-token query:
--
--   /api/v1/listings?seller=<lowercase>       -> []      (row exists)
--   /api/v1/listings/<collection>/<token>     -> 404     (row exists)
--   profile LISTINGS stat                     -> 0
--   collections.listed_count                  -> 0
--
-- while the unfiltered /listings page rendered fine because it compares no
-- address at all. Lowercase is the convention the rest of the schema already
-- assumes: profiles/saved_searches store lowercase, and the RLS policies say so
-- outright (011_rls_rework.sql:10, 024_rls_audit_fixes.sql:15).
--
-- Every statement is idempotent (`WHERE col <> lower(col)`), so re-running is a
-- no-op and rows already lowercase are never touched.
--
-- ORDER MATTERS: collections.address is the FK target for listings.collection,
-- nft_ownership.collection and auctions.collection. collections is ALREADY
-- lowercase (written by a different path), so children can be lowered directly
-- and will continue to match. Goose wraps this in a transaction.

-- +goose Up
-- +goose StatementBegin

-- Sanity guard: if collections.address were ever non-lowercase, lowering the
-- children below would break the foreign keys. Fail loudly rather than corrupt.
--
-- NOTE: 001_init.sql annotates this column "-- checksummed EIP-55", but that
-- comment is stale: the live API returns collections.address lowercase
-- (verified against production 2026-08-30), because collections are written by
-- a different path than the chain-log handlers. This guard verifies rather than
-- trusts either the comment or that observation.
DO $$
DECLARE bad INT;
BEGIN
  SELECT count(*) INTO bad FROM collections WHERE address <> lower(address);
  IF bad > 0 THEN
    RAISE EXCEPTION
      'collections.address has % non-lowercase row(s); lower collections first or the child FKs will break', bad;
  END IF;
END $$;

UPDATE listings
   SET collection = lower(collection),
       seller     = lower(seller)
 WHERE collection <> lower(collection)
    OR seller     <> lower(seller);

UPDATE nft_ownership
   SET collection = lower(collection),
       owner      = lower(owner)
 WHERE collection <> lower(collection)
    OR owner      <> lower(owner);

UPDATE auctions
   SET collection      = lower(collection),
       seller          = lower(seller),
       highest_bidder  = lower(highest_bidder)
 WHERE collection     <> lower(collection)
    OR seller         <> lower(seller)
    OR (highest_bidder IS NOT NULL AND highest_bidder <> lower(highest_bidder));

UPDATE offers
   SET collection = lower(collection),
       bidder     = lower(bidder)
 WHERE collection <> lower(collection)
    OR bidder     <> lower(bidder);

UPDATE sales
   SET collection = lower(collection),
       seller     = lower(seller),
       buyer      = lower(buyer)
 WHERE collection <> lower(collection)
    OR seller     <> lower(seller)
    OR buyer      <> lower(buyer);

UPDATE bids
   SET bidder = lower(bidder)
 WHERE bidder <> lower(bidder);

UPDATE nft_tokens
   SET collection = lower(collection),
       owner      = lower(owner)
 WHERE collection <> lower(collection)
    OR (owner IS NOT NULL AND owner <> lower(owner));

UPDATE nft_metadata
   SET collection = lower(collection)
 WHERE collection <> lower(collection);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Deliberately irreversible. Down would have to restore the original EIP-55
-- casing, which is not recoverable from the lowercased value, and restoring it
-- would re-introduce the bug this migration exists to fix. Lowercase addresses
-- are valid input to every reader, so there is nothing to undo.
SELECT 1;
-- +goose StatementEnd
