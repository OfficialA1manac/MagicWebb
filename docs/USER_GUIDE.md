# MagicWebb — User Guide

MagicWebb is a fast, **unstoppable** NFT marketplace on the [Flare](https://flare.network) network. This guide is a complete walk-through: every user action, what happens on-chain, what the backend records, and what the UI does in response. Use this when onboarding a new contributor, during customer-support escalations, or as the post-deploy smoke-test checklist.

> **Network:** MagicWebb runs on **Coston2 testnet** (chain id 114 / `0x72`). The wallet picker auto-fills the chain when you connect.

> **Fee model:** seller-pays 1.5% on every sale (deducted from seller's proceeds). **Listings, auction creation, bidding, and making offers are all free.** Sellers receive 98.5% of every sale; the platform fee is a Solidity `constant` — no admin key can change it. There is no admin, no pause, and no upgrade proxy on any contract.

---

## Quick links

- **Live site:** https://magicwebb.fly.dev/
- **Smart contracts:** `contracts/src/Marketplace.sol`, `contracts/src/AuctionHouse.sol`, `contracts/src/OfferBook.sol`
- **Backend API:** see `frontend/docs/API.md` (REST `/api/v1/*` + `/events` SSE)
- **Live indexer status:** `GET /healthz` and `GET /api/v1/indexer/status` on the deployed host

---

## Get started

1. **Open** https://magicwebb.fly.dev/.
2. **`Connect Wallet`** in the top-right. MagicWebb opens the **Reown AppKit** wallet picker (or the self-hosted **WalletConnect v2** overlay as fallback) — scan the QR code with any mobile or hardware wallet, or use a deep-link from your wallet app.
3. The wallet picker handles the Coston2 chain add automatically. If your wallet can't auto-switch, manually switch the chain to Coston2 (RPC `https://coston2-api.flare.network/ext/C/rpc`, native `C2FLR`).
4. After SIWE (Sign-In-with-Ethereum) sign-in, the navbar shows your address, the bell (notifications), and the Search bar.

> **Tip:** MagicWebb silently attempts to auto-reconnect your previously-connected wallet on page load. If the session is still valid, the saved-wallet pill collapses and you're connected. If the session expired, the saved-wallet pill stays visible with Reconnect/Forget buttons — click Reconnect to re-pair via QR.

---

## What you can do — feature flows

Every user action is documented below as a 4-step trace: **UI click → wallet JS → on-chain tx → indexer event → SSE → UI re-render**. Follow any flow top-to-bottom to understand exactly which contract function fires, which Postgres row updates, and which UI component picks up the SSE event.

### A. Connect your wallet

1. **Navbar → "Connect Wallet"** opens the Reown AppKit wallet picker (or the self-hosted WalletConnect v2 QR overlay as fallback).
2. `wallet.js :: connect()`:
   - Initialises the WalletConnect SDK (self-hosted UMD bundle) or Reown AppKit (Vite-built, ethers adapter) for the QR pairing flow.
   - Calls `eth_requestAccounts` via the connected provider.
   - Reissues SIWE (nonce + sign + verify).
   - JWT issued and stored as HttpOnly cookie `mw_s_<addr>` + in-memory Bearer header.
3. On any non-recoverable failure the toast surfaces the exact reason (domain mismatch, user rejection, chain mismatch, etc.). On success, `mw_addr` is persisted to localStorage and the **Saved wallet** pill is cleared.

### B. Browse the marketplace

| Page | URL | Backed by |
|------|-----|-----------|
| Home | `/` | `live/indexer/counts` (count + volume) |
| Listings | `/listings` | `q.ListActiveListings(f)` paginated |
| Auctions | `/auctions` | `q.ListAuctions({Status: "active"})` |
| Token page | `/token/:coll/:id` | `q.GetListing` + `q.GetTokenMeta` + `q.GetTokenAttributes` |
| Collection | `/collection/:addr` | `q.GetCollectionStats` + `q.ListActiveListings(...)` |
| Profile | `/profile/:addr` | `q.GetVerifiedPendingWithdrawal` + activity feed |

Real-time updates: every `/events` subscription joins when you load a page and fires `mw-listed`, `mw-bought`, `mw-bid-placed`, `mw-auction-created`, `mw-auction-settled`, `mw-offer-made`, `mw-offer-accepted`, `mw-offer-rejected`. The page subscribes + dispatches a partial refresh.

### C. List an NFT for fixed-price sale

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Token page → "List for sale" → modal shows price + expiry slider | — | — |
| 2 | Click "List for X FLR" | `_approveOperator(coll, MARKETPLACE, standard)` triggers `setApprovalForAll(MARKETPLACE, true)` if needed | — |
| 3 | Sign tx | `MARKETPLACE.list(coll, id, priceWei, expiresAt)` → emits `Listed(coll, id, seller, "erc721"/"erc1155", amount, price, expiresAt)` | `onListed` parses + `q.UpsertListingAndOwnership` (atomic pgx tx) → SSE `listing-updated` |
| 4 | Modal "Listed for sale" + tx hash | — | listings + collection pages refresh via SSE |

After listing you can `cancel()` any time before a buy settles, no questions asked.

### D. Buy a fixed-price listing

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | "Buy now" button | `/api/v1/listings/:coll/:id/preflight?seller=…` returns `ok`, `price_wei` | server-side `eth_call` of `ownerOf` + `isApprovedForAll` |
| 2 | Modal shows **You pay X FLR** / **Seller receives 98.5%** / **Platform fee 1.5%** | `staticCall(buy(coll, id, seller, { value }))` is INFORMATIONAL only — a flaky RPC should not block valid flows | — |
| 3 | Sign tx | `MARKETPLACE.buy(coll, id, seller, { value: priceWei })` → emits `Bought(coll, id, buyer, seller, standard, amount, price, fee)` | `onBought` runs `q.DeactivateAndSale` (atomic pgx tx) → SSE `listing-updated` + seller notification |
| 4 | Modal "Purchase confirmed" + tx hash | — | listings + token pages refresh |

The buy is **final at settlement** — there is no reverse path. NFT moves to buyer, fee to feeRecipient, 98.5% to seller.

### E. Create an auction

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Token → "Auction" tab → fill reserve, endsAt, minIncrement % (default 5%) | — | — |
| 2 | "Create auction — free" +2 approvals | `AUCTION.create(coll, id, reserveWei, endsAt, minIncBps, minIncFlat)` | `onAuctionCreated` upserts + SSE `auction-updated` |
| 3 | Modal "Auction #N is live" | — | auction page refreshes; **anti-snipe banner** if `endsAt - now < 3 min` |

### F. Bid on an auction

**Anti-snipe rule:** any new-lead bid inside the 3-minute closing window (`EXTENSION_WINDOW = 3 minutes`) pushes `endsAt` forward by 3 minutes (per `AuctionExtended` event). Sub-threshold accreting bids do NOT extend — that's the audit C-01 fix.

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Auction page → "Place a bid" | — | — |
| 2 | Modal "Bid amount" + cumulative escrow + refund-on-outbid disclaimer | — | — |
| 3 | Sign | `AUCTION.bid(auctionId, { value: bidAmountWei })` (msg.value escrowed; bidding is free on top of escrow) | `onBidPlaced` runs `q.InsertBidAndUpdateAuction` (atomic pgx tx) → SSE `auction-updated` |
| 4 | Modal "Bid placed · You are now the leading bidder" | — | leader chip switches |
| 5 | If you were outbid: notification (`outbid` kind), email-style toast | — | `onOutbidNotification` fired by the contract on every lead change; SSE |

To retake the lead, place another bid; cumulative bids add. If you exit the auction, your escrow is preserved and refunded automatically after settlement, or pull it via "Withdraw refund" any time after `endsAt`.

### G. Settle an auction (after `endsAt`)

Settlement is **permissionless**. It can happen three ways:

1. You click "Settle" on the auction page (`AUCTION.settle(id)`).
2. Any user calls `settle(id)` from their wallet.
3. The MagicWebb **keeper** broadcasts `settle` automatically every 30 s for any auction where `status='active' AND ends_at < now()`.

The keeper runs under a Postgres advisory-lock single-flight gate (`db.WaitKeeperLock`) so cluster-wide only one machine broadcasts at any time — no split-brain.

Settle paths:

| Outcome | Contract | Indexer |
|---------|----------|---------|
| **Happy** (winner exists, NFT OK) | NFT → winner; 98.5% of `effective_wei` → seller; 1.5% → recipient; `AuctionSettled(id, winner, seller, bidAmt, fee)` | `onAuctionSettled` flips status → SSE `auction-updated` + auction_won/sold notifications |
| **Seller revoked NFT / moved it** (delivery reverts) | `auction.stalledAt = block.timestamp`; `AuctionStalled(id)` emitted. After `STALL_WINDOW = 7 days`, seller can `reclaim(id)` for full escrow refund to winner | `onAuctionSettled` does NOT fire; status stays `active` for 7 days, then `reclaim` flips to `cancelled` |
| **Loser refund sweep** (post-settle) | `refundLosers(id, batch[])` with `gas: 50_000` per iter + `BatchTooLarge()` guard at 200 — losers receive escrow, greedy receivers parks in `pendingReturns` | `onLoserRefunded` seeds `pending_withdrawals`; `runWithdrawalSweeper` verifies every 2 min and fires "Action needed: withdraw your refund" notification when verified |

### H. Make an offer

Open offers are escrowed principal until accepted/rejected/expired.

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Token page (non-owner view) → "Make an offer" → principal + expiry (default 7d, capped at 14d) | — | — |
| 2 | Sign | `OFFERBOOK.makeOffer(coll, id, principal, expiresAt, { value: principal })` → `OfferMade(coll, id, bidder, principal, units, expiresAt)` | `onOfferMade` upserts offer position + notifies owner + SSE `offer-updated` |
| 3 | Owner sees offer under "Offers received" | — | — |

The escrow is fully refundable until the offer is accepted, rejected, or expires automatically (the keeper refunds every minute for expired positions).

### I. Accept an offer (owner)

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | "Offers received" → "Accept" | `_approveOperator(coll, OFFERBOOK)` then `OFFERBOOK.acceptOffer(coll, id, bidder)` → `OfferAccepted(coll, id, seller, bidder, principal, fee, units, standard)` | `onOfferAccepted` runs `q.AcceptOfferAndRecordSale` (atomic pgx tx) + bidder notification + SSE |
| 2 | Modal "Offer accepted — you received 98.5%" + tx hash | — | — |

The 1.5% fee is deducted from the seller's payout. Bidder's escrow is settled; seller gets 98.5%.

### J. Reject an offer (owner)

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | "Offers received" → "Reject" | `OFFERBOOK.rejectOffer(coll, id, bidder)` → `OfferRefunded(coll, id, bidder, principal)` (pushes or parks in `pendingReturns` for non-payable bidders) | `onOfferRefunded` flips status + bidder notification + SSE |
| 2 | Modal "Offer rejected — bidder refunded" | — | — |

### K. Cancel a listing (seller)

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Token page (owner) → "Cancel" | `MARKETPLACE.cancel(coll, id)` → `Cancelled(coll, id, seller)` | `onCancelled` deactivates listing + SSE |
| 2 | Modal "Listing cancelled" | — | same |

Listing cancel is **gas-free** for the seller. Your NFT immediately stops being purchasable.

### L. Cancel an auction early

Only allowed **before any bid**. After the first bid lands, the auction is locked.

| Step | UI | Contract | Indexer |
|------|-----|----------|---------|
| 1 | Auction page (owner, no bids) → "Cancel early" | `AUCTION.cancelEarly(id)` → `AuctionCancelled(id)` | `onAuctionCancelled` flips status + SSE |

### M. Withdraw my refund

If a settlement's refund push failed (your wallet is a contract without `receive()` or the refundee OOMed), the credit is parked in `AuctionHouse.pendingReturns(address)`. The withdrawal sweeper verifies on-chain every 2 min and notifies you.

| Step | UI |
|------|-----|
| 1 | Profile banner "Action needed: withdraw your refund" → "Withdraw" |
| 2 | `AUCTION.withdrawRefund()` — sender reads `pendingReturns(self)` first, surfaces the amount in the modal |
| 3 | Modal "Refund withdrawn · X FLR sent to your wallet" + tx hash |

### N. View your activity

Profile page (`/profile/:addr`) shows **owned**, **listings**, **offers made**, **offers received**, **bids**, **won**, **sold**, **pending withdrawals**, **notifications**. Each tab is paged; every entry is live via `/events`.

### O. Search

`/search?q=...` hits `/api/v1/search`, runs Postgres full-text search across NFT names + collection names. Sectioned by NFTs then Collections; `Enter` opens the result.

### P. Report content

"⋮" menu → "Report" → form (target type, target id, reason, detail). `POST /api/v1/reports`. JWT-protected.

---

## What to do when something goes wrong

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Buy modal says "listing no longer fillable" | The seller cancelled, or someone else bought first, or the seller moved the NFT out of their wallet | Refresh; consider placing an offer instead |
| Bid modal says "BidTooLow" | Below the leading cumulative + `minIncrement` | Compute the minimum required bid in the modal and bump up |
| Bid modal says "AuctionLive" | You tried to settle before `endsAt` | Wait for the countdown to hit zero |
| Withdraw modal says "Nothing to withdraw" | On-chain `pendingReturns(self)` already zero | No action — your push went through; this banner was stale |
| Connect modal says "Wallet chain did not switch" | Your wallet's auto-add path silently failed | Manually switch your wallet to Coston2 (chain id 114 / 0x72) |
| Listings/auctions page stuck loading | SSE disconnected mid-stream; partial refresh pending | Refresh the page; the connection will re-establish |
| `site.xy/api/v1/auctions` returns 502 | The indexer is behind. Keepers re-attempt via the `backfill()` retry pattern. Should self-heal within 1–2 RPC cycles (≤ 4 s) | Wait then retry; if persistent > 30 s, page on-call |

---

## Reference — quick links

- **Audit ledger:** `docs/AUDIT.md` — every finding from C-01 through R-04 with severity, status, and verification
- **Deployment checklist:** `docs/DEPLOY_CHECKLIST.md` — Coston2 deployment and env var reference
- **Immutability transition:** `docs/IMMUTABILITY_TRANSITION.md` — contract immutability notes for Coston2
- **Monitoring runbook:** `docs/MONITORING.md` — per-alert-class actions and event cheat-sheet
- **API reference:** `frontend/docs/API.md` — all REST endpoints, auth flow, and rate limits
- **Technical whitepaper:** `frontend/docs/WHITEPAPER_TECHNICAL.md` — contract design and threat model
- **FAQ:** `frontend/docs/FAQ.md` — common questions and troubleshooting

## Reference — event-to-feature map

A canonical mapping for ops + customer support:

| User action | Contract function | Event(s) emitted | SSE event | DB write |
|-------------|-------------------|-----------------|-----------|----------|
| List NFT | `MARKETPLACE.list` | `Listed` | `listing-updated` | `listings`, `nft_ownership` (atomic) |
| Cancel listing | `MARKETPLACE.cancel` | `Cancelled` | `listing-updated` | `listings.active=false` |
| Buy | `MARKETPLACE.buy` | `Bought` | `listing-updated` | `listings.active=false`, `sales` (atomic), notify seller |
| Create auction | `AUCTION.create` | `AuctionCreated` | `auction-updated` | `auctions` |
| Bid | `AUCTION.bid` | `BidPlaced`, `OutbidNotification` (on lead change), `AuctionExtended` (new-lead anti-snipe) | `auction-updated` | `bids`, `auctions.highest_*` (atomic), notify displaced |
| Settle | `AUCTION.settle` | `AuctionSettled`, `LoserRefunded[]`, `RefundPushed[]` | `auction-updated`, `notification` | `auctions.status='settled'`, `pending_withdrawals` seed |
| Make offer | `OFFERBOOK.makeOffer` | `OfferMade` | `offer-updated` | `offers`, notify owner |
| Accept offer | `OFFERBOOK.acceptOffer` | `OfferAccepted` | `offer-updated` | `offers.status='accepted'`, `sales` (atomic), notify bidder |
| Reject offer | `OFFERBOOK.rejectOffer` | `OfferRefunded` | `offer-updated` | `offers.status='cancelled'`, notify bidder |
| Auto-refund offer | `OFFERBOOK.refundExpiredOffer` | `OfferRefunded` | `offer-updated` | `offers.status='cancelled'`, notify bidder |
| Withdraw refund | `AUCTION.withdrawRefund` | — (no event) | `notification` (from sweeper) | `pending_withdrawals` verified + owner reads `pendingReturns` on-chain |
| NFT transfer | ERC-721 / ERC-1155 | `Transfer` / `TransferSingle` / `TransferBatch` | `listing-updated` | `nft_ownership` update; old seller's listings orphaned |

---

## Live test matrix (post-v21)

Every row was verified via browser-test on `https://magicwebb.fly.dev/` (no console errors, every page returns 200 OK, SSE active, /healthz returns OK):

| User flow | Status |
|-----------|--------|
| Home renders; counts match | ✓ |
| Listings page lists active listings | ✓ |
| Auction page lists active auctions | ✓ |
| Token page renders attributes + attributes grid | ✓ |
| Profile renders owned + listings + activity | ✓ |
| `/events` SSE opens | ✓ |
| MODAL_OPTS_FALLBACK for malformed dispatches | ✓ |
| WalletConnect v6 positive-command protocol — spinner + URI → QR | ✓ (manual) |
| SIWE signature failure surfaces typed error | ✓ |
| Chain-switch retry × 3 × 200 ms | ✓ |

---

## See also

- `docs/AUDIT.md` — defect ledger including the Priority Stack walkthrough of every fix
- `docs/WHITEPAPER.md` — economics + design philosophy
- `docs/WHITEPAPER_TECHNICAL.md` — technical architecture deep-dive
- `docs/FAQ.md` — frequently asked questions
- `docs/DEPLOY_FLY.md` — Fly.io deployment shape and rollback recipe
