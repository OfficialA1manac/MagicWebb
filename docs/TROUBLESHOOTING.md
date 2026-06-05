# MagicWebb — Troubleshooting & Operations

Practical recovery steps with **real defaults** for the Flare **Coston2** deployment (chain `114`).
The app is a single Go binary; contract addresses live in `.env` (backend) and in
`backend/internal/ui/static/wallet.js` (frontend).

---

## 1. "Wrong network" (chain id ≠ 114)

The UI is wired to **Flare Coston2**, chain id `114`, native **C2FLR**. Use **Switch to Coston2**
in the app; if your wallet has never seen Coston2, use **Add Coston2 to wallet** first.

Manual add (MetaMask, etc.):

| Field | Value |
| --- | --- |
| Network name | Flare Coston2 |
| Chain ID | `114` (decimal) |
| Symbol | C2FLR |
| Decimals | 18 |
| RPC URL | `https://coston2-api.flare.network/ext/C/rpc` |
| Block explorer | `https://coston2-explorer.flare.network` |

Testnet faucet: <https://faucet.flare.network> (select Coston2).

---

## 2. `NotApproved` (list / buy / accept offer)

ERC-721/1155 requires an explicit **operator approval** for the contract that will move the NFT.
The in-app flows include an **Approve …** step where relevant.

| Action | Approve this operator on the **NFT collection** contract |
| --- | --- |
| Fixed-price list / buy | `Marketplace` — `setApprovalForAll(<MARKETPLACE_ADDR>, true)` |
| Create auction / settle | `AuctionHouse` — `setApprovalForAll(<AUCTION_ADDR>, true)` |
| Accept offer | `OfferBook` — `setApprovalForAll(<OFFERBOOK_ADDR>, true)` |

Addresses come from `.env` (`MARKETPLACE_ADDR`, `AUCTION_ADDR`, `OFFERBOOK_ADDR`) and must match
the constants embedded in `wallet.js`.

---

## 3. `Expired` (buy / offer)

- **Buy:** the listing's on-chain expiry passed; `buy` reverts. The seller should **cancel** (if
  still listed) and **list again** with a new expiry.
- **Offer:** the offer's on-chain `expiresAt` passed. The bidder can reclaim the escrowed ETH via
  **`refundExpiredOffer`** (permissionless — anyone can trigger the refund to the original bidder)
  and then make a fresh offer with a later deadline.

---

## 4. Auction: I was outbid — where are my funds?

When you are outbid, `AuctionHouse` refunds your bid automatically in the same transaction as the
new high bid. If that push refund ever fails, the amount is credited to `pendingReturns[you]` as a
**pull-pattern** fallback. Open **Profile** with your wallet connected — the app requests
**`withdrawRefund()`** automatically when you have a claimable balance.

---

## 5. Offer didn't go through / "insufficient value"

`OfferBook` is fully **on-chain and escrowed** — there are no off-chain signatures or nonces.
`makeOffer` must be sent with `value = principal + 1.5% fee`. If the value is short, the tx
reverts. Use the in-app **Make Offer** flow, which computes the fee for you (`withFee()` in
`wallet.js`).

---

## 6. `eth_getLogs` "requested too many blocks … maximum is …"

The public Coston2 JSON-RPC limits how many blocks one `eth_getLogs` call may span (often ~30).
The **indexer** already chunks requests; tune in `.env`:

- `GETLOGS_CHUNK` (default `30`)
- `GETLOGS_BLOCK_CAP` (default `30`; set `0` only with your own node / higher-limit RPC)
- `INDEX_FROM_BLOCK` controls where a fresh index starts.

---

## 7. OneDrive / cloud sync (Windows)

If the repo lives under `OneDrive\` (or Dropbox/iCloud), partial sync can corrupt `bin/` or
`contracts/out`. Exclude build output from sync, or clone under a non-synced path
(e.g. `C:\dev\MagicWebb`). `make clean` regenerates all build artifacts.

---

## 8. Still stuck?

- Confirm RPC + contract addresses in `.env` match your deployment and `wallet.js`.
- Check the transaction on the [Coston2 explorer](https://coston2-explorer.flare.network) for the
  revert reason.
- See `PLATFORM.md` for operations, `SYSTEM.md` / `CONTRACTS_ANNOTATED.md` for contract behavior.
