# MagicWebb — End-to-End Walkthrough

How the application works, told three ways over the same flows:

- **User** — what you click and see.
- **Developer** — the code path, file by file.
- **Backend response** — what the server does for each action.

The single most important thing to understand: **marketplace state changes never POST to the
backend.** The wallet signs a transaction straight to the contract; the chain is the source of
truth; the indexer *observes* events and projects them into Postgres, then pushes a live update
over SSE. The only authenticated writes the backend accepts are **off-chain social/metadata**
(profile, reports, notification reads, admin) — never listings, buys, bids, or offers.

```
 User action ─▶ wallet.js ─▶ ethers ─▶ CONTRACT (on-chain)
                                            │ emits event
                       indexer watcher ◀────┘  (polls every 2s, eth_getLogs)
                            │ decode + DB write (pgx)
                            ▼
                       Postgres  ──▶ SSE broadcast ──▶ browser (live refresh)
                                 └─▶ HTMX page reads on next navigation
```

---

## 0. Connect + sign in (SIWE)

**User:** Click **Connect** → pick MetaMask or WalletConnect → approve → sign a "Sign-In with
Ethereum" message (no gas). You're now authenticated; an avatar/menu appears.

**Developer:**
1. `wallet.js → connect(kind)` opens the provider (injected or `@walletconnect/ethereum-provider`),
   then `wallet_addEthereumChain` / switch to chain `114`.
2. Frontend `GET /auth/nonce?address=0x…` → server returns a random nonce (`main.go:nonceHandler`,
   stored in the in-memory `nonce.Store` with a 5-min TTL; rate-limited 20/min/IP).
3. Wallet signs the SIWE message containing that nonce.
4. `POST /auth/verify {address, message, signature}` → `main.go:verifyHandler`:
   - `nonce.GetDel(addr)` consumes the nonce (single-use replay protection),
   - `verifyEIP191()` recovers the signer from the signature and checks it equals `address`,
   - `auth.Issue()` returns an HS256 JWT. Stored in `localStorage` as `mw_jwt` + `mw_kind`.

**Backend response:** Issues a JWT bound to the lowercased wallet address. That token is sent as
`Authorization: Bearer …` on the four authenticated endpoints only.

---

## 1. List an NFT (fixed price) — free

**User:** Open **Profile / wallet picker**, choose an NFT, set a price, click **List**. First a
one-time **Approve** (per collection), then **List**. Listing is free — no fee on listing.

**Developer:**
1. NFT picker is filled by `GET /api/v1/wallet/:addr/nfts` (`api/…walletNFTs`).
2. `wallet.js → list(collection, tokenId, price)`:
   - if needed, `collection.setApprovalForAll(MARKETPLACE, true)`,
   - `Marketplace.list(...)` (non-payable; min price 0.01, max 90-day expiry).
3. Contract emits `Listed(address,uint256,address,uint8,uint128,uint128,uint64)`.
4. Indexer watcher (`indexer/runner.go:runWatcher`, 2-s tick) picks up the log via
   `eth_getLogs` filtered on the three contracts + `coreTopics()` (`indexer/abis.go`),
   `handlers.dispatch()` decodes `TopicListed` and upserts the listing row (`db/queries*.go`).
5. `sse.Broadcaster.Publish({Type:"listing-updated"})` → all open `/events` streams receive it.

**Backend response:** No write endpoint is hit during listing. The listing appears in the DB only
after the indexer sees the on-chain event, then fans out a `listing-updated` SSE event.

---

## 2. Buy a listing — taker pays +1.5%

**User:** On a listing, click **Buy**. The wallet shows `price + 1.5%`. Confirm. Moments later the
item shows **Sold** and the seller/you get a notification.

**Developer:**
1. `wallet.js → buy(collection, tokenId, seller, priceWei)`:
   - **Preflight:** `GET /api/v1/listings/:collection/:id/preflight` checks the listing is still
     live (not sold/expired/orphaned) before spending gas.
   - `withFee(priceWei)` = `priceWei + priceWei*150/10000`.
   - `Marketplace.buy(collection, tokenId, seller, {value: withFee})`.
2. Contract (non-custodial): pulls the NFT from the seller → buyer, sends `price` → seller and the
   `1.5%` → immutable `feeRecipient`, deletes the listing, emits
   `Bought(address,uint256,address,address,uint8,uint128,uint128,uint256)`.
3. Indexer decodes `TopicBought` → marks listing sold + records the sale + writes notifications.
4. SSE `activity` + `listing-updated` events → live UI update; the buyer's/seller's in-app
   notification count bumps.

**Backend response:** Read-only `preflight` is the *only* backend call in the buy path. Everything
else is chain → indexer → DB → SSE. The `Bought` event is what drives "Sold", the activity feed,
and notifications.

---

## 3. Auction: create → bid → settle

**User:** **Create auction** (start price, duration ≤ 7 days). Bidders **Bid** (`bid + 1.5%`);
each new high bid refunds the previous bidder automatically. When time's up, the auction **settles
itself** (a keeper does it) — winner gets the NFT, seller gets the proceeds.

**Developer:**
1. `wallet.js → bid(auctionId, bidAmount)` → `AuctionHouse.bid(..., {value: withFee(bidAmount)})`.
   The UI warns if you bid inside the anti-snipe window (the contract extends the end time).
2. Events `AuctionCreated` / `BidPlaced` / `AuctionSettled` / `AuctionCancelled` are decoded by the
   indexer → auction + bid rows + `auction-updated` SSE.
3. **Settlement (keeper):** if `KEEPER_KEY` is set, `runAuctionKeeper` (30-s tick) queries
   `GetExpiredActiveAuctions` and sends a raw `settle(uint256)` tx (`sendSettle`); it also
   cancels auctions left inactive (`GetInactiveAuctions`).
4. Auction countdowns use `GET /api/v1/server-time`, which returns the latest block timestamp the
   watcher records atomically (`serverTimeMs`) — clock synced to chain, not the browser.

**Backend response:** Reads serve auction/bid lists; the keeper is the only component that *writes
to the chain*, and only to call `settle`. If no keeper runs, auctions remain unsettled until
someone calls `settle` manually — an operational dependency (see `READINESS.md`).

---

## 4. Offers: make → accept / expire (on-chain escrow)

**User:** On any token, **Make Offer** with an amount + expiry; your ETH (`amount + 1.5%`) is
**escrowed on-chain**. The owner can **Accept** (you get the NFT, they get the ETH) or **Reject**.
If it expires, the escrow is refundable.

**Developer:**
1. `wallet.js → makeOffer(collection, tokenId, principal, expiresAt)` →
   `OfferBook.makeOffer(..., {value: withFee(principal)})`. Stacked positions: multiple offers per
   token are allowed. **No EIP-712 signatures or nonces** — it's real escrowed ETH.
2. Events `OfferMade` / `OfferAccepted` / `OfferRefunded` → indexer → offer rows + `offer-updated` SSE.
3. **Expiry:** the DB-side `runOfferExpirySweeper` (5-min tick) marks offers expired; if a keeper
   runs, `runOfferKeeper` (60-s tick) calls `refundExpiredOffer(address,uint256,address)` on-chain
   to return escrow to bidders. `refundExpiredOffer` is permissionless, so anyone can also trigger it.

**Backend response:** Reads serve the offer book and a wallet's position
(`GET /api/v1/offers/:collection/:id/position`). Refund automation is the keeper; the backend never
holds custody of offer funds.

---

## 5. The few things the backend actually writes (authenticated)

These are off-chain and require a valid JWT (`jwtMiddleware` in `api/rest.go`):

| Endpoint | Purpose |
|----------|---------|
| `PUT /api/v1/profile/:addr` | Edit your own profile (display name, bio, socials). |
| `GET/POST /api/v1/notifications[/read]` | List + mark in-app notifications read. |
| `POST /api/v1/reports` | Trust & safety reports. |
| `POST /api/v1/admin/verify` | Collection verification — gated by `ADMIN_ALLOWLIST`. |

Everything else under `/api/v1/*` is **public, read-only**, rate-limited 60/min/IP, serving the
projected read model (listings, auctions, offers, collections, trending, search, metrics, activity,
wallet NFTs, indexer status).

---

## 6. Live updates & background workers

- **SSE hub** (`internal/sse`): in-memory fan-out. `GET /events` streams `listing-updated`,
  `auction-updated`, `offer-updated`, and `activity`, with a 15-s keepalive. Slow clients are
  skipped, never block the publisher.
- **Indexer workers** (`internal/indexer/runner.go`), all goroutines off one binary:
  - **watcher** — backfills from the last indexed block, then polls head every 2 s; also tracks
    ERC-721/1155 `Transfer` events on tracked collections to maintain ownership and **orphan**
    listings whose seller moved the NFT out.
  - **score worker** (60 s) — recomputes trending scores over 1h/24h/7d windows.
  - **offer-expiry sweeper** (5 min) — marks expired offers in the DB.
  - **metadata worker** — fetches token metadata/images.
  - **auction keeper** + **offer keeper** (only if `KEEPER_KEY` set) — on-chain settlement/refunds.

## 7. Page rendering (HTMX)

Pages (`/`, `/listings`, `/auctions`, `/auction/:id`, `/offers`, `/profile/:addr`,
`/collection/:addr`, `/token/:addr/:id`, `/search`, `/metrics`) are server-rendered from embedded
templates (`cmd/server/ui.go` + `internal/ui`). `hx-get` partials (`/partials/listings`,
`/partials/auctions`, `/partials/activity`) return HTML fragments so lists refresh without a full
page load; the SSE stream nudges them to re-fetch when on-chain state changes.
