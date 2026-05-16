# MagicWebb — platform guide

This document is for **developers**, **collectors**, **collection creators / sellers**, and the **broader community**. It explains what MagicWebb is, what you can do, and how the pieces fit together.

---

## 1. One product, two layers (no separate “backend”)

MagicWebb is intentionally small: **a web app plus smart contracts**. There is **no** standalone API server, database, or indexer required for trading to work.

| Layer | Role |
| --- | --- |
| **Frontend** (`frontend/`) | Next.js UI, wagmi/viem, wallet connection. Reads chain state and submits transactions. |
| **Contracts** (`contracts/src/`) | Source of truth: listings, auctions, offers, fees, pausing. |

Anything that looks like a “backend” in other marketplaces (order books, relayers, custody) is either **on-chain** here or **off-chain but non-custodial** (e.g. passing a signed offer JSON between people).

**How they work as one system**

1. You connect a wallet (browser extension or WalletConnect).
2. The app configures RPC + contract addresses from env (`frontend/.env.local`).
3. Every buy, list, bid, settle, deposit, or accept runs as **your** transaction to the Flare RPC — the UI is only a signer and a reader.
4. If the website went offline, **your assets and listings still exist on-chain**; anyone could build another UI or use a block explorer with the same ABIs.

---

## 2. For collectors (buyers & bidders)

- **Buy a listed NFT** — Search → collection → token. If listed and not expired, **Buy now** sends `msg.value` equal to the list price. The marketplace transfers the NFT and splits the fee.
- **Bid in an auction** — Auctions page or token flow. Each bid must beat the previous by the minimum increment. If you are outbid, your funds move to **pending returns**; withdraw them from **Profile**.
- **Make a signed offer** — On a token page (even if not listed), connect, optionally **deposit** into OfferBook, then **Make offer**. You sign EIP-712 data; copy the JSON and send it to the owner off-chain. They import it under **Offers → Received** and accept on-chain when they agree.

**Networks** — Default deployment targets **Flare Coston2** (chain id 114). Use a Coston2–funded wallet and the in-app network switcher when prompted.

---

## 3. For creators & sellers (projects and individuals)

- **List a fixed price** — **List NFT** in the nav (or `/list`): enter ERC-721 contract + token id, connect as owner, approve **Marketplace**, set price and expiry, list.
- **Run an auction** — Same flow; approve **AuctionHouse**, set reserve, duration, and min increment (bps). Public settlement after `endsAt`.
- **Offers** — You do not “list” an offer on-chain. Bidders sign; you **accept** when you like the price. Keep your OfferBook approval in mind when you are ready to accept.

**Fees** — A platform fee is enforced in the contracts (default 2.5%, hard-capped at 10%). The fee is applied on each settled trade.

**ERC-1155** — Contracts support 1155; the `/list` wizard currently focuses on **ERC-721** first. Advanced flows can use the same ABIs with amount parameters from the contracts.

---

## 4. For developers

- **Repo layout** — `frontend/` (Next 15), `contracts/` (Foundry), `docs/` (this file + whitepapers + annotated Solidity), root `Makefile` (bash) for deploy/lifecycle, root `package.json` to run the app from **repo root**.
- **Env** — Single file for the app: `frontend/.env.local` (copy from `frontend/.env.example`). `NEXT_PUBLIC_*` keys are inlined at build time; **never** commit real private keys.
- **WalletConnect / Reown** — Set `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID` and `NEXT_PUBLIC_APP_URL` for QR and metadata.
- **Local dev from root** — `npm start` or `npm run dev` at the repository root runs `next dev` inside `frontend/`.
- **Contracts** — `cd contracts && forge test && forge build`. Deploy script: `DeployCoston2.s.sol` (see `Makefile` `deploy` target when using bash + Foundry).

**EIP-712 domain** — The on-chain `OfferBook` domain name is **`MagicWebbOfferBook`** (version `1`). The frontend `lib/eip712.ts` **must** match the deployed contract or signatures will not verify.

---

## 5. For the community & operators

- **Open source** — MIT. Issues and PRs welcome on [GitHub](https://github.com/OfficialA1manac/MagicWebb).
- **Security** — Non-custodial design reduces platform risk but not smart-contract risk. Treat testnet as experimental; mainnet requires your own audit and key custody plan.
- **Transparency** — Contract addresses for Coston2 are shown on the home page and in `README.md`; verify bytecode on a Flare explorer before large flows. Operational fixes (chunks, chain 114, approvals, refunds) live in [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md).

---

## 6. Glossary

| Term | Meaning |
| --- | --- |
| **Coston2** | Flare testnet (chain id 114). |
| **Pull refund** | Outbid auction funds are credited to you; you call `withdrawRefund` instead of receiving ETH inside the bid tx. |
| **EIP-712** | Typed structured data signing; wallets show readable offer fields before you sign. |
| **OfferBook deposit** | Shared escrow balance used when your accepted offers consume your committed amount. |

For contract-level detail, read `WHITEPAPER_TECHNICAL.md` and `CONTRACTS_ANNOTATED.md`.
