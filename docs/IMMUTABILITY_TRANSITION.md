# MagicWebb v29 — Coston2 → Mainnet Immutability Transition

> The system is currently on Coston2 (chainId 114) for live pre-mainnet
> verification. This document is the **immutable transition** narrative:
> the exact sequence of operations between "Coston2 quorum met" and
> "mainnet passkey discarded". Source of truth: `contracts/AUDIT_REPORT.md`
> Phase 4d + Phase 6.

## Pre-conditions (every item is a hard gate)

- [ ] **All 146 foundry tests + 1 invariant pass** on a fresh clone
      after `forge build`. Coverage report attached in `audit/forge-coverage.txt`.
- [ ] **Slither reports zero findings** against the post-Round-3 + v29
      working tree (`slither . --filter-paths 'lib/|test/' 2>&1 | tail`).
- [ ] **Live Coston2 dry-run** of `contracts/script/e2e_coston2.sh`
      completes every step with the museum of expected events:
      `Listed`, `Bought`, `AuctionCreated`, `BidPlaced`, `AuctionSettled`,
      `OfferMade`, `OfferAccepted`, `WithdrawRefunded`, `PushFailed`.
      Keeper fires on every settlement; SSE broadcasts reach
      `magicwebb.fly.dev/events` within 2 s.
- [ ] **Cross-stack parity verified**: contracts at v28, backend at v29,
      frontend at v28.0.2 — same layer-set the audit used.
      `docs/DEPLOY_CHECKLIST.md` smoke-matrix passes.
- [ ] **Final audit sign-off**: `contracts/AUDIT_REPORT.md` "Final
      Security Posture" reads PRODUCTION-READY; tester-of-record
      signs the doc on a per-deploy basis (the audit report is NOT a
      one-shot — every mainnet deploy refs the most-recent dated
      report).

## Mainnet multisig pattern (mandatory)

| Role                      | Mainnet holder                | Powers                                            |
|:--------------------------|:------------------------------|:--------------------------------------------------|
| `<CONTRACT_ADMIN>`        | Gnosis Safe 1-of-N (N ≥ 3)    | `grantRole(KEEPER_ROLE, …)`, `renounceRole`        |
| `<FEE_RECIPIENT>`         | Gnosis Safe treasury          | receives 1.5% platform fees                        |
| `<KEEPER_BOT>`            | Hot wallet OR server-side ECDSA | broadcasts `settle()` + `refundExpiredOffer()`     |
| `<KEEPER_KEY>` (env)      | server-pinned, **never** human-readable | construct `DynamicFeeTx`                     |

**Mandate**: admin keys MUST be multisig (1-of-3 minimum) so no single
key compromise can halt entries. EXIT paths (settle, refund,
withdrawRefund) require NO admin role, so even in the worst case
"all admin keys lost" the marketplace keeps paying out.

## Constructor wiring

```solidity
// contracts/script/DeployFlare.s.sol
MarketplaceCore.constructor(
    address feeRecipient,   // <FEE_RECIPIENT> multisig
    address manager         // <CONTRACT_ADMIN> multisig (zero = ungated Coston2)
);
MarketplaceManager.constructor(<CONTRACT_ADMIN>); // grants
                                                  // DEFAULT_ADMIN_ROLE
                                                  // to deployer — see below
```

After deployment:

1. `MarketplaceManager.grantRole(KEEPER_ROLE, <KEEPER_BOT>)`
2. `MarketplaceManager.renounceRole(DEFAULT_ADMIN_ROLE, deployer)` —
   **deployer address must NOT retain the admin role on mainnet**.

## Role renounce dance (the most-skipped step)

The deployer's public key is well-known and a frequent target. After
the multisig takes possession:

```bash
# Step 1: grant admin role to multisig
cast send <MARKETPLACE_MANAGER> \
  "grantRole(bytes32,address)" \
  $(cast keccak "DEFAULT_ADMIN_ROLE") \
  <CONTRACT_ADMIN_MULTISIG> --private-key $DEPLOYER_KEY

# Step 2: verify the multisig can call admin
cast call <MARKETPLACE_MANAGER> \
  "hasRole(bytes32,address)(bool)" \
  $(cast keccak "DEFAULT_ADMIN_ROLE") \
  <CONTRACT_ADMIN_MULTISIG>
# → true

# Step 3: renounce deployer admin
cast send <MARKETPLACE_MANAGER> \
  "renounceRole(bytes32,address)" \
  $(cast keccak "DEFAULT_ADMIN_ROLE") \
  $(cast wallet address --private-key $DEPLOYER_KEY) \
  --private-key $DEPLOYER_KEY

# Step 4: verify the deployer has nothing
cast call <MARKETPLACE_MANAGER> \
  "hasRole(bytes32,address)(bool)" \
  $(cast keccak "DEFAULT_ADMIN_ROLE") \
  $(cast wallet address --private-key $DEPLOYER_KEY)
# → false
```

**At this point, the deployer private key is waste**. Burn it
(re-export from a clearing key derivation path that has no other
use) and store the deployment artifact (contract addresses +
verified source) on the project ledger.

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

## Why this document

The `$75k+ full-stack audit` directive (Phase 6) requires that the
immutable transition from "testable" to "immutable" be DECLARED in
the audit folder with an externally-verifiable checklist. Three
items are **mechanically verifiable** (forge tests pass, slither
clean, multisig role renounce; the verification is replayable from a
clone). Two items are **operationally verifiable** (live Coston2
dry-run, smoke-matrix pass; replay requires running the harness).
The auditor signs both halves before clearing for mainnet.

## Counter-signed releases (audit-grade binding)

| Auditee | Audit pass | Date | Ledger hash | Audit doc |
|:--------|:-----------|:-----|:------------|:----------|
| <tester-of-record> | v29 Round 4 review | ________ | __________ | `contracts/AUDIT_REPORT.md` |

When the tester's signature + a `keccak256(working-tree)` is recorded
alongside the deploy address, the marketplace is "mainnet-ready" by
the audit ledger's terms.

## End-state

When this document is counter-signed and the chain-final bytecode
exists, the only forward-improvements are FRONTEND-only. The
contracts are immutable. The backend is replaceable (Fly.io
deployment can swap to a new region or roll back to a previous
release), but it does NOT mutate the contracts' behavior because
every backend operation reads from the same on-chain source-of-truth.
The keeper wallet is hot but bounded by `KEEPER_MAX_FEE_CAP_GWEI` /
`KEEPER_MAX_TIP_CAP_GWEI` ceilings (`docs/DEPLOY_CHECKLIST.md`
Phase 3) so a compromise caps loss per settlement.
