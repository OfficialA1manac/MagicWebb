# MagicWebb
### A Non-Custodial NFT Marketplace for the Flare Network

**Version 1.0 — May 2026**

---

## Executive summary

MagicWebb is a non-custodial NFT marketplace built natively for the Flare Network. It supports the two dominant token standards — ERC-721 and ERC-1155 — across three trade primitives: instant fixed-price purchase, English auction, and signed off-chain offers. The platform is engineered for the reality of a sovereign-data network: low fees, transparent settlement, and zero custodial risk. MagicWebb charges a single hardcoded 1.5% platform fee across all operations (listing, buying, auctions, offers). The fee rate is a Solidity `constant` — no admin key, environment variable, or upgrade path can change it.

This document explains what MagicWebb is, who it serves, why it exists, and where it is going. A separate **technical whitepaper** (`WHITEPAPER_TECHNICAL.md`) covers the smart-contract design and threat model in depth; a **line-by-line annotation** (`CONTRACTS_ANNOTATED.md`) walks every line of every Solidity file.

---

## 1. The problem

NFT marketplaces today suffer from three structural failures:

1. **Custodial risk.** Many incumbents take custody of NFTs to support delayed-settlement features. When the marketplace fails (insolvency, hack, regulatory seizure), users lose both the asset and any unsettled proceeds.
2. **Hidden fees.** "2.5% platform fee" headlines often hide a 5–10% effective take-rate after sequencer fees, MEV extraction, and floor-price spread games.
3. **Single-standard assumptions.** Most user interfaces are designed around either ERC-721 (one-of-one art) or ERC-1155 (semi-fungible items). Switching standards forks the entire product surface.

MagicWebb addresses all three:

- **No custody.** Sellers retain the NFT in their wallet until the moment a buyer settles. The contract holds zero NFT inventory at rest.
- **Fixed fee.** The on-chain `PLATFORM_FEE_BPS` constant is 150 basis points (1.5%). It is a `constant` — not a variable, not a mutable slot. No admin can change it post-deploy.
- **One product, two standards.** A single contract surface handles both ERC-721 and ERC-1155 via a one-byte discriminator. Frontends treat them uniformly.

---

## 2. The opportunity

The Flare Network is a layer-1 blockchain purpose-built to bring high-integrity data on-chain — price feeds via FTSO, programmatic state attestations, and cross-chain proofs. As Flare's native asset (FLR) ecosystem matures, demand for an NFT venue that respects Flare's design principles — open data, decentralised oracles, low cost — has outpaced the available supply.

MagicWebb is positioned as the canonical NFT trade venue on Flare:

- **Native FLR settlement.** No wrapped-token detour. Buy, sell, and bid in the network's native unit.
- **FTSO-backed pricing (optional).** USD-equivalent display can use Flare Time-Series Oracle feeds when the UI wires them in; the marketplace contracts settle in native token only.
- **Coston2-first.** Full feature parity on testnet from day one. Mainnet promotion gated on independent audit and multisig handover.

---

## 3. How it works (in plain English)

### Buying

1. Browse listings on the MagicWebb web app.
2. Click **Buy**. Your wallet pops up; confirm the transaction.
3. The NFT lands in your wallet. The seller is paid. The platform fee goes to the fee wallet. All in one atomic transaction — either every part succeeds or nothing happens.

### Selling

1. Connect your wallet. Approve the marketplace once per collection.
2. Choose **List** (fixed price), **Auction** (English with reserve), or wait for **Offers** (someone else proposes a price).
3. The NFT stays in your wallet until a buyer claims it. Cancel any time.

### Offers (the bidder side)

1. Deposit some FLR into the OfferBook contract once.
2. Sign offers off-chain (no gas, no per-offer fee).
3. If your offer is accepted, the contract debits your deposit and transfers the NFT to you.
4. Withdraw any unspent deposit any time.

### Auctions (the bidder side)

1. Place a bid. Your FLR is locked in the auction contract.
2. If you are outbid, your refund balance grows in the contract. Withdraw it any time.
3. If you win, anyone (you, the seller, a bystander) can call **settle** after the end time. The NFT comes to you; the seller is paid.

---

## 4. What makes MagicWebb different

### 4.1 Single hardcoded fee

The `PLATFORM_FEE_BPS` constant in `MarketplaceCore.sol` is 150 (1.5%). It is a `constant` — not a variable, not a mutable storage slot. It is charged only on a successful sale (a fixed-price buy, auction settlement, or offer acceptance) and deducted from the seller's proceeds; listing, auction creation, bidding, and making offers are free. Changing the rate requires deploying a new contract with a different address, so users can verify it themselves.

Fees are sent directly to the `feeRecipient` wallet via `.call{value: fee}("")` — no intermediary contract, no vault, no accumulator step.

This is a structural commitment, not a marketing line.

### 4.2 No custody

NFTs are never escrowed. The marketplace contract holds an `ERC1155Holder` interface only so that future workflows can route 1155s through the contract if needed; in normal operation, every transfer goes seller → buyer in a single call inside the same buy/settle/accept transaction.

If MagicWebb's frontend disappears tomorrow, your NFT stays in your wallet. You can interact with the contracts directly via Etherscan/Flarescan or any ABI-compatible tool.

### 4.3 Pull-pattern refunds

In a naive auction contract, an outbid bidder is paid back inside the new bidder's transaction. A malicious contract bidder can refuse the refund — DOS-ing every outbid attempt — and lock the auction at their bid price.

MagicWebb credits outbid amounts to a per-bidder balance (`pendingReturns`). The original bidder calls `withdrawRefund()` themselves to claim. No one can block anyone else.

### 4.4 Anti-snipe

Last-second bids would be disheartening for both bidders and sellers. MagicWebb extends the auction by 5 minutes if a winning bid arrives in the last 5 minutes. Each subsequent qualifying bid extends again. The auction ends when bids stop arriving in the window — a fair market clearing rather than a contest of latency.

### 4.5 Hybrid standards

Whether you are trading a 1-of-1 generative art piece (ERC-721) or 1,000 collectible cards (ERC-1155), the same UI and the same contracts apply. We avoid the common trap of building a "PFP marketplace" and bolting a "gaming items" tab on later.

---

## 5. Architecture (high-level)

```
On-chain (Flare Coston2)
   ├── Marketplace        — fixed-price listings
   ├── AuctionHouse       — English auctions
   ├── OfferBook          — signed off-chain offers
   └── MarketplaceCore    — shared fee, pause, transfer logic

Off-chain
   ├── Indexer (Go + Zig) — listens to events, populates Postgres
   ├── API (Go + GraphQL) — read API for the frontend, SIWE auth
   └── Frontend (Next.js) — wagmi-powered web app
```

The off-chain layer is a convenience layer for fast browsing. Trading is on-chain; the off-chain layer cannot censor, freeze, or front-run a trade.

---

## 6. Security posture

MagicWebb has been:
- **Statically analysed** with Slither (latest stable). All real findings patched; remaining detector hits are accepted-design (paying authenticated parties, timestamp use for expiry checks).
- **Manually audited** by claude-opus-4-7 against the standard NFT-marketplace threat list (reentrancy, signature replay, integer truncation, fee-cap bypass, DOS-by-revert, sniping).
- **Tested** with 49 forge tests across all 5 contracts, including dedicated regressions for every patched finding.

Pre-mainnet, MagicWebb will commission an independent professional audit and move admin keys to a multisig.

---

## 7. Token-economic model

MagicWebb **does not have a token**. There is no ICO, no airdrop, no governance token. The platform fee is collected in FLR on each settled trade.

This is a deliberate choice:

- A token would create an alignment between the platform and short-term price action that we do not want. We are building infrastructure, not a meme.
- The Flare Network already has FLR; introducing a second token to a niche venue would fragment liquidity.
- All decisions that *could* be governed (pauser changes, fee recipient address) are visible on-chain via the AccessControl events. The fee rate itself cannot be governed — it is a compile-time constant.

If user feedback ever justifies governance, it will be added via a separate mechanism — not retroactively jammed in by minting a token.

---

## 8. Roadmap

| Phase | Quarter | Scope |
|---|---|---|
| **Phase 1** | Q2 2026 (current) | Coston2 testnet. All trade primitives live. Indexer + frontend feature-complete. |
| **Phase 2** | Q3 2026 | Independent audit. Multisig admin handover. Mainnet deploy. ERC-2981 royalty integration. FTSO USD display. |
| **Phase 3** | Q4 2026 | Cross-collection routing. Bundle listings. WFLR/USDC payment adapter for stable-priced offers. |
| **Phase 4** | 2027 | Songbird deploy. Cross-network offer relay via Flare FAssets. Bug bounty live. |

---

## 9. Risks and mitigations (user-facing)

| Risk | What happens | What we do |
|---|---|---|
| Smart-contract bug | Funds at risk | Hard fee cap, audit, bug bounty, no upgradeability that could change behavior post-deploy |
| Operator key compromised | Admin role changes | Fee rate is a hardcoded constant — no admin can change it; worst case is pausing the contracts; pre-mainnet handover to multisig |
| Frontend disappears | Inconvenient | Contracts remain callable via Flarescan or any wallet's "contract interaction" panel |
| Indexer lags | Stale UI | Frontend shows "syncing" badge; on-chain trades still settle in real time |
| Approval phishing | Drained NFTs | Per-collection approvals; user education in app; we never request blanket setApprovalForAll for unknown contracts |

---

## 10. Team and origins

MagicWebb is being built by an independent contributor in 2026 as a public-good NFT venue for Flare. The codebase is open source under MIT. Contributions, audit reports, and feedback are welcome via the project repository.

---

## 11. Closing

The NFT space spent its first cycle on speculation, the second on infrastructure, and now needs venues that respect both the assets and the people trading them. MagicWebb is one such venue: small in scope, bounded in promises, and verifiable end-to-end.

> "Make the simple thing easy and the complex thing possible — and let the user verify everything."

---

**Find us:** github.com/<repo>
**Read the code:** `contracts/src/`, `frontend/`
**Read the technical whitepaper:** `WHITEPAPER_TECHNICAL.md`
**Read every line:** `CONTRACTS_ANNOTATED.md`
