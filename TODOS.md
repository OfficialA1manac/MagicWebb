# TODOS

## Deploy / Mainnet

### Mainnet launch gates
**Priority:** P1
Safe multisig as `CREATOR_ADDR` (DeployFlare.s.sol enforces contract address),
external audit sign-off, dedicated RPC endpoints in `RPC_URLS`. See
docs/PERFORMANCE_AUDIT.md decision record.

## Completed

### Coston2 v2 redeploy (seller-pays contracts)
Deployed 2026-06-10, block 31610228 — Marketplace
`0x6E5d2332a30bE3aBC35a0dC795583a06cfedFc9C`, AuctionHouse
`0xAF76199b6BB77fEB1E1D8541C30557f3231F6F5c`, OfferBook
`0x9D38CB500551BfDD106CA8052C9Bfd146A5f6CbA`, Manager
`0xFDcAE06C12d5E54Ff3304B29C415F006B553627F`. Fee recipient + admin handover
verified on-chain; keeper registered under KEEPER_ROLE. Deploy runbook:
docs/DEPLOY_FLY.md. **Completed:** 2026-06-10.

### Surface pendingReturns as "withdraw required" (review IN-03)
Events seed `pending_withdrawals`; 2-min sweeper verifies on-chain via
`pendingReturns()`, profile banner + one-time notification on confirmed balances.
**Completed:** 2026-06-10.

### Verified-collection badges
Migration 012 `collections.verified`, listing-card + collection-header badges,
`POST /api/v1/admin/collections/verify` (allowlist+SIWE). **Completed:** 2026-06-10.

### HTMX action sheet (mobile)
Already present in the v2 token page ("Manage this NFT") — TODO was based on
assuming it only existed on the old main line. **Completed:** pre-existing.

### WalletConnect support
Connector + provider already in wallet.js; completed the missing plumbing
(`WC_PROJECT_ID` env → config → layout `window.MW_WC_PROJECT_ID`). Also fixed
wallet.js hardcoding the DEAD v1 contract addresses — addresses now injected
from server config. **Completed:** 2026-06-10.

### Port atomic tx wrappers from PR #1
`onBought` now uses the transactional `DeactivateAndSale` (seller-scoped — the
PR #1 version would have deactivated other holders' stacked 1155 listings);
`onBidPlaced` already used `InsertBidAndUpdateAuction`. pgxmock test pins the
seller-scoped WHERE. **Completed:** 2026-06-10.
