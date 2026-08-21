# Contract deployment checklist

Companion to `contracts/AUDIT_REPORT.md`. Use for every network. Items marked **mainnet**
are hard gates for Flare (chain 14).

## Before
- [ ] `forge test` green (115 tests incl. fuzz + invariants); `slither` clean or triaged.
- [ ] **mainnet** External audit report covers the exact commit being deployed.
- [ ] **mainnet** `CREATOR_ADDR` is a Gnosis Safe (`contracts/script/DeploySafe.s.sol`),
      not an EOA. Threshold ≥ 2.
- [ ] Deployer EOA funded for ~5 UUPS proxy deployments + wiring calls.
- [ ] `KEEPER_ADDR` decided (the backend's `KEEPER_KEY` address) and funded
      (`KEEPER_MIN_BALANCE_WEI`, default 0.1 native).
- [ ] RPC endpoints for the network confirmed (`deployments/<network>.json → rpc`).

## Deploy
```bash
cd contracts
PRIVATE_KEY=0x… CREATOR_ADDR=0x… KEEPER_ADDR=0x… \
forge script script/Deploy<Network>.s.sol --rpc-url <rpc> --broadcast -vvvv
```
The script itself asserts, on-chain, after deploying:
`feeRecipient == creator` on all three market contracts, `manager` wiring,
`entriesAllowed()`, creator holds `DEFAULT_ADMIN_ROLE` + `OPERATOR_ROLE`, deployer has
renounced both, keeper holds `KEEPER_ROLE`.

## After
- [ ] Copy addresses + deploy block into `deployments/<network>.json`; run
      `tools/check-deployments.sh`.
- [ ] Tag `vX.Y.Z` so CI verifies source on the explorer.
- [ ] Create Neon project; set Fly secrets; set GitHub variables; deploy
      (`docs/DEPLOY_FLY.md`).
- [ ] Smoke: `/healthz`, `/readyz`, `/api/v1/indexer/slo` head lag < 30 blocks.
- [ ] One real list → buy round-trip from two wallets.
- [ ] Add origin to `NETWORK_URLS` on the other networks' apps.
- [ ] `docs/IMMUTABILITY_TRANSITION.md` — decide the upgrade-delay posture.
