# TODOS

## Indexer

### Surface pendingReturns as "withdraw required" (review IN-03)
**Priority:** P2
After `LoserRefunded`/`RefundPushed` events, cross-check
`AuctionHouse.pendingReturns(bidder)` via `CallContract`; flag rows where the push
failed so the UI shows a "withdraw required" state instead of "refunded".
`wallet.js` already exposes `withdrawRefund()`.

## Frontend

### Verified-collection badges
**Priority:** P2
Port from `origin/main` `ad06c3e`: `tracked_collections.verified` column (new
migration on OUR schema), join through CollectionRow/ListingRow, green check on
listing cards + collection page. Copy must say seller-pays (the source commit's
"+1.5% per bid" wording is the dead taker-fee model — do not port the copy).

### HTMX action sheet (mobile)
**Priority:** P2
Port the action-sheet component from `origin/main` `fa49414` onto the current
templates.

### WalletConnect support
**Priority:** P3
`origin/main` threads `WCProjectID` through config → layout → wallet.js. Port the
plumbing and add WalletConnect as a connector alongside injected wallets.

## Deploy / Mainnet

### Mainnet launch gates
**Priority:** P1
Safe multisig as `CREATOR_ADDR` (DeployFlare.s.sol enforces contract address),
external audit sign-off, dedicated RPC endpoints in `RPC_URLS`. See
docs/PERFORMANCE_AUDIT.md decision record.

## Completed

### Port atomic tx wrappers from PR #1
`onBought` now uses the transactional `DeactivateAndSale` (seller-scoped — the
PR #1 version would have deactivated other holders' stacked 1155 listings);
`onBidPlaced` already used `InsertBidAndUpdateAuction`. pgxmock test pins the
seller-scoped WHERE. **Completed:** 2026-06-10.
