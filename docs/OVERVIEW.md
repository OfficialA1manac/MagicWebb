# MagicWebb — Project Overview

MagicWebb is a single marketplace application: **frontend + contracts only**.

## What it does

- Lists ERC-721/1155 tokens for fixed-price sales.
- Runs English auctions with anti-sniping and pull-based refunds.
- Supports EIP-712 signed offers accepted on-chain.

All trading logic is on-chain. The frontend reads/writes directly against deployed contracts through wallet + RPC.

## Runtime architecture

```
Wallet <-> Next.js frontend (wagmi/viem) <-> Flare RPC <-> Contracts
```

There is no backend API/indexer dependency for marketplace flow correctness.

## The smart contracts (4 files)

All in `contracts/src/`:

### 1. `MarketplaceCore.sol` (the abstract base)

Provides the things every trade contract needs:
- **Fee handling** — `feeBps` (basis points), capped at 10%, plus the `_splitAndPay(seller, amount)` helper that collects the platform fee and sends the remainder to the seller.
- **Pause switch** — admin can pause new trades (existing trades still settle).
- **Roles** — `DEFAULT_ADMIN_ROLE`, `FEE_ROLE`, `PAUSER_ROLE`. Granted to the deployer at construction; should be moved to a multisig before mainnet.
- **Standard-aware transfer** — `_transferToken(standard, coll, from, to, id, amount)` calls the right function for ERC-721 vs ERC-1155.
- **Reentrancy guard** — provides the `nonReentrant` modifier to children.

It is `abstract` because it does not provide trade entry points itself — children do.

### 2. `Marketplace.sol` (fixed-price listings)

- `list(coll, id, price, expiresAt)` — list an ERC-721 at a fixed price.
- `list1155(coll, id, amount, price, expiresAt)` — list `amount` units of an ERC-1155.
- `cancel(coll, id)` — seller cancels their listing.
- `buy(coll, id) payable` — anyone buys at the listed price.

Listings live in a mapping keyed by `(collection, tokenId)`. Two storage slots per listing.

### 3. `AuctionHouse.sol` (English auctions)

- `create(...)` / `create1155(...)` — seller starts an auction with reserve, start/end times, min-increment percentage.
- `bid(id) payable` — place a bid. Outbid amounts go to a per-bidder refund balance.
- `withdrawRefund()` — outbid bidders pull their refunds.
- `settle(id)` — anyone can call after `endsAt`. NFT goes to winner, payment goes to seller.
- `cancel(id)` — seller cancels an auction with no bids.

Two key features:
- **Pull-pattern refunds** — prevents a malicious bidder from blocking outbids.
- **Anti-snipe** — a winning bid in the last 5 minutes extends the auction by 5 minutes.

### 4. `OfferBook.sol` (signed off-chain offers)

The most off-chain-heavy contract:

- `deposit() payable` / `withdraw(amount)` — bidders escrow ETH once.
- The bidder signs a typed `Offer` struct off-chain (no gas).
- `acceptOffer(offer, sig, tokenIdActual)` — token owner submits the offer + signature; contract verifies, transfers NFT, debits bidder's deposit.
- `acceptOffer1155(offer, sig)` — same flow for ERC-1155.
- `cancelOffer(nonce)` — bidder pre-emptively burns a nonce.

Uses **EIP-712** for typed signatures so wallet UIs can render the offer in a human-readable way before signing.

### 5. Platform fee

Each trade contract enforces an immutable platform fee (`feeBps` in `MarketplaceCore`). On each successful settlement, `_splitAndPay` collects the platform fee and forwards the remainder to the seller atomically.

---

## The frontend (`frontend/`)

Next.js 15 App Router. Notable pieces:

- `app/page.tsx`, `app/collection/[addr]/page.tsx`, `app/token/[addr]/[id]/page.tsx` — main browse/detail pages.
- `app/profile/[addr]/page.tsx` — user's listings, bids, offers.
- `app/auction/[id]/page.tsx` — single auction view.
- `components/ConnectButton.tsx`, `NetworkGuard.tsx` — wagmi wallet UI + chain check.
- `components/ListingCard.tsx`, `AuctionCard.tsx` — repeating tile UI.
- `components/OfferModal.tsx`, `BidForm.tsx`, `BuyButton.tsx` — action triggers.
- `hooks/use*.ts` — one hook per contract action (`useList`, `useBuy`, `useBid`, `useSignOffer`, `useAcceptOffer`, ...).
- `lib/wagmi.ts` — Coston2 chain config + wallet connector setup.
- `lib/eip712.ts` — domain + type definitions for `OfferBook` signatures. **Must mirror the contract's typehash exactly.**
- `lib/abi/Marketplace.ts` (and friends) — generated TS ABIs for type-safe `writeContract` calls.

---

## End-to-end flow: a listing sale

1. Alice owns NFT #42 in collection `0xAbc...`.
2. Alice opens MagicWebb and connects her wallet.
3. Alice clicks **List**, enters price = 1 FLR, expiry = 7 days. The frontend:
   - calls `useApproveNFT` if the marketplace doesn't yet have approval,
   - calls `useList` → wagmi `writeContract` → wallet pop-up.
4. Alice confirms. The tx hits Coston2; `Marketplace.list` runs:
   - validates ownership and approval,
   - blocks if a different seller already has the slot (`AlreadyListed`),
   - writes the listing, emits `Listed`.
5. Bob loads the collection page and reads directly from chain-backed calls in the frontend.
6. Bob clicks **Buy**. Wallet pop-up. Bob confirms.
7. `Marketplace.buy{ value: 1 FLR }(coll, 42)` runs:
   - validates listing exists, not expired, msg.value == price,
   - deletes the listing slot (CEI),
   - `_transferToken` moves NFT from Alice → Bob,
   - `_splitAndPay` collects 0.025 FLR platform fee, 0.975 FLR to Alice,
   - emits `Bought`.
8. Frontend refreshes from contract reads after receipt.

The whole sale is one Coston2 transaction. No partial state. If any step fails, the entire tx reverts and Bob's FLR stays in his wallet.

---

## End-to-end flow: an offer accepted

1. Carol wants to buy NFT #42, but Bob (the new owner) hasn't listed it.
2. Carol opens MagicWebb, deposits 1.2 FLR into `OfferBook` via `deposit()`. (One-time, refundable.)
3. Carol clicks **Make offer** for NFT #42, amount 1.1 FLR, expires in 24 h. Frontend:
   - constructs the `Offer` typed-data,
   - calls `useSignOffer` → wagmi `signTypedData` → wallet pop-up.
4. Carol signs. **No gas spent.** The signature + offer struct can be shared directly with the owner.
5. Bob clicks **Accept**. Wallet pop-up.
6. `OfferBook.acceptOffer(offer, sig, 42)` runs:
   - rejects if offer.amount == 0 (audit-patch),
   - rejects if expired or nonce already used,
   - rejects if `tokenIdActual` doesn't match the offer's tokenId (or 0 = collection-wide),
   - `ECDSA.recover(digest, sig) == offer.bidder`,
   - validates Bob's ownership and approval,
   - debits Carol's deposit by 1.1 FLR,
   - `_transferToken` moves NFT from Bob → Carol,
   - `_splitAndPay` collects 0.0275 FLR platform fee, 1.0725 FLR to Bob,
   - emits `OfferAccepted`.
7. Carol's remaining deposit (0.1 FLR) stays in `OfferBook`; she can `withdraw` any time.

---

## End-to-end flow: an auction settles

1. Dave creates an auction for NFT #99, reserve 0.5 FLR, ends in 1 h, min increment 5%.
2. Eve bids 0.5 FLR. Becomes highest bidder.
3. Frank bids 0.6 FLR. Becomes highest bidder. Eve's 0.5 FLR is credited to her in `pendingReturns`.
4. Eve calls `withdrawRefund()`; her 0.5 FLR returns to her wallet.
5. With 4 minutes left, Grace bids 0.7 FLR. Anti-snipe triggers: `endsAt` extends to `now + 5 min`. `AuctionExtended(id, newEnd)` event emitted.
6. No further bids. `endsAt` arrives.
7. Anyone (Dave, Grace, a watcher script) calls `settle(id)`:
   - `_transferToken` moves NFT from Dave → Grace,
   - `_splitAndPay` collects 0.0175 FLR platform fee, 0.6825 FLR to Dave,
   - emits `AuctionSettled`.
8. Frank's 0.6 FLR is in `pendingReturns`; he withdraws when convenient.

---

## What's where (project tree)

```
MagicWebb/
├── contracts/
│   ├── src/                   ← 4 production Solidity files
│   │   ├── MarketplaceCore.sol
│   │   ├── Marketplace.sol
│   │   ├── AuctionHouse.sol
│   │   └── OfferBook.sol
│   ├── test/                  ← forge tests
│   ├── script/                ← deploy scripts
│   └── foundry.toml
├── frontend/
│   ├── app/                   ← Next.js App Router pages
│   ├── components/            ← UI components
│   ├── hooks/                 ← one hook per contract action
│   ├── lib/                   ← wagmi config, eip712 helpers, ABIs
│   ├── .env.example           ← env template (copy → .env.local)
│   └── package.json
├── docs/
│   ├── WHITEPAPER.md             ← marketing whitepaper
│   ├── WHITEPAPER_TECHNICAL.md   ← technical whitepaper
│   ├── CONTRACTS_ANNOTATED.md    ← line-by-line Solidity walkthrough
│   └── OVERVIEW.md               ← this file
├── Makefile                   ← start / stop / restart / status / health
```

---

## How to run it locally

1. `cp frontend/.env.example frontend/.env.local`
2. `make install`
3. `make start`
4. Open `http://127.0.0.1:3000`
5. `make stop` when done.

---

## Where to dig deeper

- **Roles & product overview:** `PLATFORM.md`
- **Marketing-level overview:** `WHITEPAPER.md`
- **Technical whitepaper:** `WHITEPAPER_TECHNICAL.md`
- **Solidity, line by line:** `CONTRACTS_ANNOTATED.md`

---

## Glossary (one line each)

- **ERC-721** — one-of-one NFT standard.
- **ERC-1155** — multi-edition NFT standard (you can own 5 of the same token).
- **EIP-712** — typed structured-data signature standard. Wallets render the data for the user.
- **CEI** — Checks → Effects → Interactions. Reentrancy-safe ordering.
- **FTSO** — Flare Time-Series Oracle. Native price feeds.
- **bps** — basis points. 100 bps = 1%. 250 bps = 2.5%.
- **Coston2** — Flare's primary testnet (chain id 114).
- **Pull pattern** — instead of pushing payments to users (which can fail and revert the whole tx), credit them to a balance they pull themselves.
- **Anti-snipe** — extending an auction's end time when a bid arrives in the last few minutes.
