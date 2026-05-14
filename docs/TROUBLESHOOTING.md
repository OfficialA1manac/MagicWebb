# MagicWebb — troubleshooting & operations

This page expands the short bullets in `README.md` with **real defaults** for the Flare **Coston2** deployment and practical recovery steps.

---

## 1. `Cannot find module './NNNN.js'` (webpack / `.next` chunks)

**What it is:** Next.js split the server or client bundle into numbered chunks; something deleted, overwrote, or half-synced files under `frontend/.next` while `next dev` or `next start` was using them.

**Fix (always try this first):**

```bash
# From repo root (either works)
npm run dev:clean

# Or from frontend/
cd frontend && npm run clean && npm run dev
```

**Rules:**

- Do **not** run `next dev` and `next start` against the **same** `frontend/.next` tree without running `npm run build` in between for production mode.
- After changing `NEXT_PUBLIC_*` env vars, restart the dev server so the client bundle is rebuilt.

**OneDrive / cloud sync:** If the repo lives under `OneDrive\` (or Dropbox, iCloud Drive, etc.), **exclude** `frontend/.next` from sync, or clone the repo under a non-synced path (e.g. `C:\dev\MagicWebb`). Partial sync is a common cause of missing chunk files on Windows.

---

## 2. “Wrong network” (chain id ≠ 114)

MagicWebb’s UI is wired to **Flare Coston2**, **chain id `114`**, native **C2FLR**.

**In the app:** Use **Switch to Coston2**. If your wallet has never seen Coston2, use **Add Coston2 to wallet**, then switch.

**Manual add (MetaMask, etc.):**

| Field | Value |
| --- | --- |
| Network name | Flare Coston2 |
| Chain ID | `114` (decimal) |
| Symbol | C2FLR |
| Decimals | 18 |
| RPC URL | `https://coston2-api.flare.network/ext/C/rpc` (must match `NEXT_PUBLIC_RPC_URL` in your env) |
| Block explorer | `https://coston2-explorer.flare.network` |

Testnet faucet: [https://faucet.flare.network](https://faucet.flare.network) (select Coston2).

---

## 3. `NotApproved` (list, buy path, accept offer)

ERC-721 requires an explicit **operator approval** for the contract that will move the NFT.

| Action | Approve this operator on the **NFT collection** contract |
| --- | --- |
| Fixed-price list / buy | `Marketplace` — `setApprovalForAll(<MARKETPLACE_ADDR>, true)` |
| Create auction / bid / settle | `AuctionHouse` — `setApprovalForAll(<AUCTION_ADDR>, true)` |
| Accept signed offer | `OfferBook` — `setApprovalForAll(<OFFER_ADDR>, true)` |

Addresses come from your `frontend/.env.local` (`NEXT_PUBLIC_MARKETPLACE_ADDR`, `NEXT_PUBLIC_AUCTION_ADDR`, `NEXT_PUBLIC_OFFER_ADDR`). The in-app flows include **Approve …** steps where relevant.

---

## 4. `Expired` (buy / offer)

- **Buy:** The listing’s `expiresAt` is on-chain; after that time `buy` reverts. The seller should **cancel** (if still listed) and **list again** with a new expiry.
- **Offer:** The EIP-712 `expiresAt` in the signed payload passed; the bidder must sign a **new** offer with a later deadline.

---

## 5. `OfferUsed`

The offer’s **nonce** was already consumed (accepted) or **cancelled** by the bidder. Generate a new offer with a **fresh nonce** and sign again.

---

## 6. Auction: outbid but “no refund”

Outbids use a **pull pattern**: funds are credited to `pendingReturns[yourAddress]` in `AuctionHouse`. They are **not** sent automatically to your wallet.

**Claim:** Open **Profile** (or `/profile/me`) → **Withdraw refund** — that calls `withdrawRefund()` on `AuctionHouse`.

---

## 7. Still stuck?

- Confirm RPC and contract addresses in `frontend/.env.local` match your deployment.
- Check the transaction on [Coston2 explorer](https://coston2-explorer.flare.network) for the revert reason.
- See `docs/PLATFORM.md` for product flows and `docs/WHITEPAPER_TECHNICAL.md` for contract-level behavior.
