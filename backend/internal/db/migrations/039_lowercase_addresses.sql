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
-- nft_ownership.collection and auctions.collection, so collections is merged
-- to lowercase FIRST (see below — it can hold BOTH casings of one contract,
-- so this is a merge, not an update), then children are lowered, then the
-- checksummed parent rows are deleted. Goose wraps this in a transaction.

-- +goose Up
-- +goose StatementBegin

-- collections FIRST: address is the FK target for every child table below,
-- so a checksummed collections row must become lowercase before children are
-- lowered, or the child updates violate their FKs.
--
-- A plain UPDATE is NOT enough. Observed on coston2 (2026-08-31, the boot
-- failure that took the network down): the indexer wrote the CHECKSUMMED row
-- (carrying deploy_block, verified, and all children) and a second write path
-- later inserted an EMPTY lowercase row for the same contract — so lowering
-- the checksummed key collides with the shell's PK. Merge instead:
--
--   1. upsert a lowercase twin for every checksummed row, folding the real
--      row's data into the shell (verified/deploy_block/… win over defaults);
--   2. repoint children at the lowercase twin (the child UPDATEs below —
--      their FK now resolves because the twin exists);
--   3. delete the checksummed parent, which is childless after (2).
--
-- tracked_collections is handled here too: both casings of the same contract
-- were tracked, so lowering the checksummed one would collide with its PK.
-- search_vec is a GENERATED column and must not appear in the insert list.
INSERT INTO collections (address, name, symbol, standard, deploy_block, tracked,
                         created_at, verified, chain_id,
                         standard_verified, verification_checked_at, creator_addr)
SELECT lower(address), name, symbol, standard, deploy_block, tracked,
       created_at, verified, chain_id,
       standard_verified, verification_checked_at, creator_addr
  FROM collections
 WHERE address <> lower(address)
ON CONFLICT (address) DO UPDATE SET
  -- The checksummed row is the indexer's (real deploy_block, real verification);
  -- the lowercase shell was inserted with defaults. Keep the strongest value.
  verified          = collections.verified OR EXCLUDED.verified,
  standard_verified = collections.standard_verified OR EXCLUDED.standard_verified,
  deploy_block      = CASE WHEN collections.deploy_block = 0
                           THEN EXCLUDED.deploy_block
                           ELSE collections.deploy_block END,
  creator_addr      = COALESCE(collections.creator_addr, EXCLUDED.creator_addr),
  created_at        = LEAST(collections.created_at, EXCLUDED.created_at);

DELETE FROM tracked_collections t
 WHERE t.address <> lower(t.address)
   AND EXISTS (SELECT 1 FROM tracked_collections d WHERE d.address = lower(t.address));
UPDATE tracked_collections SET address = lower(address) WHERE address <> lower(address);

-- Same defensive shape for children keyed by (collection, token_id): if a row
-- somehow exists under BOTH casings, keep the lowercase one and drop the
-- checksummed duplicate rather than colliding mid-migration.
DELETE FROM nft_tokens t
 WHERE t.collection <> lower(t.collection)
   AND EXISTS (SELECT 1 FROM nft_tokens d
                WHERE d.collection = lower(t.collection) AND d.token_id = t.token_id);
DELETE FROM nft_metadata t
 WHERE t.collection <> lower(t.collection)
   AND EXISTS (SELECT 1 FROM nft_metadata d
                WHERE d.collection = lower(t.collection) AND d.token_id = t.token_id);
DELETE FROM nft_attributes t
 WHERE t.collection <> lower(t.collection)
   AND EXISTS (SELECT 1 FROM nft_attributes d
                WHERE d.collection = lower(t.collection) AND d.token_id = t.token_id
                  AND d.trait_type = t.trait_type);

UPDATE nft_attributes
   SET collection = lower(collection)
 WHERE collection <> lower(collection);

UPDATE royalties
   SET collection = lower(collection)
 WHERE collection <> lower(collection);

UPDATE trending_scores
   SET collection = lower(collection)
 WHERE collection <> lower(collection);

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

-- Every child now points at the lowercase twin, so the checksummed parent is
-- childless — remove it. This is the last statement touching collections, so
-- a FK violation here means a child table was missed above: fail loudly.
DELETE FROM collections WHERE address <> lower(address);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Deliberately irreversible. Down would have to restore the original EIP-55
-- casing, which is not recoverable from the lowercased value, and restoring it
-- would re-introduce the bug this migration exists to fix. Lowercase addresses
-- are valid input to every reader, so there is nothing to undo.
SELECT 1;
-- +goose StatementEnd
