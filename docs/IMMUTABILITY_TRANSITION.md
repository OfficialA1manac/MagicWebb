# MagicWebb — Immutability & Deployment Notes (Coston2)

> The system operates exclusively on **Coston2 testnet** (chainId 114).
> This document records the deployment sequence and security posture
> for the Coston2 marketplace. No mainnet deployment is planned.

## Pre-conditions (every item is a hard gate)

- [ ] **All Foundry tests + invariants pass** on a fresh clone
      after `forge build`.
- [ ] **Slither reports zero findings** against the working tree
      (`slither . --filter-paths 'lib/|test/' 2>&1 | tail`).
- [ ] **Live Coston2 dry-run** of `contracts/script/e2e_coston2.sh`
      completes every step with the expected events:
      `Listed`, `Bought`, `AuctionCreated`, `BidPlaced`, `AuctionSettled`,
      `OfferMade`, `OfferAccepted`, `WithdrawRefunded`, `PushFailed`.
      Keeper fires on every settlement; SSE broadcasts reach
      `magicwebb.fly.dev/events` within 2 s.
- [ ] **Cross-stack parity verified**: all layers in sync.
      `docs/DEPLOY_CHECKLIST.md` smoke-matrix passes.

## Multi-sig pattern (recommended for Fee Recipient)

| Role                      | Holder                         | Powers                                            |
|:--------------------------|:-------------------------------|:--------------------------------------------------|
| `<FEE_RECIPIENT>`         | Multi-sig treasury             | receives 1.5% platform fees                        |
| `<KEEPER_BOT>`            | Hot wallet OR server-side ECDSA | broadcasts `settle()` + `refundExpiredOffer()`     |
| `<KEEPER_KEY>` (env)      | server-pinned, **never** human-readable | construct `DynamicFeeTx`                     |

EXIT paths (settle, refund, withdrawRefund) require NO admin role,
so even in the worst case "all keys lost" the marketplace keeps paying out.

## Source verification (Flare block explorer)

FlareScan / Blockscout-style verifier requires either:

- **Single-file flattened source** — `forge flatten
  contracts/src/MarketplaceCore.sol > flattened.sol` — recompile +
  re-verify the deployed bytecode matches the file.
- **Multi-file standard-json input** — `forge build --build-info` —
  upload the JSON and let the verifier match the deployed bytecode
  to the source tree.

**Recommendation**: multi-file standard-json. Flattened sources hide
the inheritance graph; standard-json preserves it for future
auditors.

## Post-deploy monitoring wiring

See [`MONITORING.md`](./MONITORING.md).

## End-state

When the contracts are deployed, the only forward-improvements are FRONTEND-only.
The contracts are immutable. The backend is replaceable (Fly.io
deployment can swap to a new region or roll back to a previous
release), but it does NOT mutate the contracts' behavior because
every backend operation reads from the same on-chain source-of-truth.
The keeper wallet is hot but bounded by `KEEPER_MAX_FEE_CAP_GWEI` /
`KEEPER_MAX_TIP_CAP_GWEI` ceilings (`docs/DEPLOY_CHECKLIST.md`
Phase 3) so a compromise caps loss per settlement.
