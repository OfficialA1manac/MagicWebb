# Contract deployment checklist

Companion to `contracts/AUDIT_REPORT.md`. Use for every network. Items marked **mainnet**
are hard gates for Flare (chain 14).

## Before
- [ ] `forge test` green (unit + fuzz + invariants); `slither` clean or triaged.
- [ ] **mainnet** External audit report covers the exact commit being deployed.
- [ ] **mainnet** `ADMIN_ADDR` is a Gnosis Safe (`contracts/script/DeploySafe.s.sol`),
      not an EOA. Threshold ≥ 2, 2–3 owners saved offline (`UPGRADE_RUNBOOK.md` → "Safe as admin").
- [ ] **mainnet** `FEE_RECIPIENT_ADDR` decided (a Safe is recommended).
- [ ] **mainnet** `KEEPER_KEY` generated with
      `(cd backend && go run ./cmd/keeperrotate -gen -out ../keeper-<network>.key)` (prints the
      address = `KEEPER_ADDR`), stored ONLY as the app's Fly secret, key file deleted; the keeper
      is funded from the deployer right after the deploy (≈ 50 native on a mainnet).
- [ ] **mainnet** Deployer funded for the 7 CREATEs of `DeployV34` (plain manager + 3 impls +
      3 UUPS proxies; no wiring calls) + keeper funding (≈ 7.7 native at 650 gwei for the
      deploy alone; 15 SGB / 15 FLR is comfortable).
- [ ] **mainnet** RPC primary + fallbacks answer `eth_chainId` today; `deployments/<network>.json`
      and `backend/internal/chain/profile/<network>.go` agree (the profile test enforces it).
- [ ] **mainnet** Neon snapshot schedule set on the network's project (`RUNBOOK_RESTORE.md`).
- [ ] Deployer EOA funded for the 7-CREATE deploy (a few native tokens on a testnet).
- [ ] `KEEPER_ADDR` decided (the backend's `KEEPER_KEY` address) and funded
      (`KEEPER_MIN_BALANCE_WEI`, default 0.1 native).
- [ ] RPC endpoints for the network confirmed (`deployments/<network>.json → rpc`).

## Deploy
```bash
cd contracts
# EVERY network (Coston2, Songbird, Flare) -- owner directive 2026-09-02:
# deploy admin-held and INSTANTLY upgradeable (upgradeDelay()==0 everywhere).
# ADMIN_ADDR is that network's saved admin wallet; immutability is a LATER,
# explicit per-network renounceAdmin() on the owner's go-immutable order.
PRIVATE_KEY=0x… ADMIN_ADDR=0x… FEE_RECIPIENT_ADDR=0x… KEEPER_ADDR=0x… \
forge script script/DeployV34.s.sol --rpc-url <rpc> --broadcast -vvvv
# (SEAL=true remains available for a deliberate sealed-from-block-one deploy;
# it requires FEE_RECIPIENT_ADDR to be a Safe/contract and ignores ADMIN_ADDR.)
```
The script itself asserts, on-chain, after deploying: `feeRecipient` matches
on all three market contracts, `manager` wiring, `keeper()` == KEEPER_ADDR
and the `hasRole` shim answers for it (load-bearing: the keeper is a
settlement authority), and — sealed — that `admin() == address(0)` so no
authority of any kind survives the deploy transaction.

## After
- [ ] Copy addresses + deploy block into `deployments/<network>.json`; run
      `tools/check-deployments.sh`.
- [ ] Tag `vX.Y.Z` so CI verifies source on the explorer.
- [ ] Create Neon project; set Fly secrets; set GitHub variables; deploy
      (`docs/DEPLOY_FLY.md`).
- [ ] Smoke: `/healthz`, `/readyz`, `/api/v1/indexer/slo` head lag < 30 blocks.
- [ ] One real list → buy round-trip from two wallets.
- [ ] Add origin to `NETWORK_URLS` on the other networks' apps (`deploy.yml` derives it from
      `deployments/*.json`; set `<NET>_ENABLED=true`).
- [ ] **mainnet** `tools/check-governance.sh` / `GET /api/v1/governance`: `admin` is the Safe
      (`admin_is_contract: true`), `keeper` is `KEEPER_ADDR`, `upgrade_delay` 0; `/status`
      "Admin & upgrades" shows the deploy-time `KeeperSet` (if the trail is empty:
      `cd backend && CHAIN_ID=<id> POSTGRES_URL=… RPC_URL=… MARKETPLACE_ADDR=… AUCTION_ADDR=…
      OFFERBOOK_ADDR=… MARKETPLACE_MANAGER_ADDR=… go run ./cmd/reindexgov -from <deploy block>`).
- [ ] **mainnet** Delete the idle EU Neon projects only after a separate, explicit owner OK.
- [ ] `docs/IMMUTABILITY_TRANSITION.md` — the network stays admin-held (instant upgrades) until
      the owner's explicit go-immutable order (`renounceAdmin()`).

## Coston2 redeploy after the duration ABI change (2026-08-21)

`list/list1155/batchList`, `create/create1155`, `makeOffer/makeOffer1155` changed signature
(`uint64 duration` instead of `uint64 expiresAt`). The live Coston2 set predates this and
cannot accept listings at all. Redeploy:

```bash
cd contracts
export PRIVATE_KEY=0x…            # funded Coston2 deployer
export ADMIN_ADDR=0x…             # single admin (testnet iteration authority)
export FEE_RECIPIENT_ADDR=0x…     # fee recipient (can be an EOA on testnet)
export KEEPER_ADDR=0x…            # address of the backend KEEPER_KEY
forge script script/DeployV34.s.sol \
  --rpc-url https://coston2-api.flare.network/ext/C/rpc --broadcast -vv
```

Then:
1. Copy the five addresses + deploy block into `deployments/coston2.json`
   (`make load-addrs` parses `contracts/broadcast/.../114/run-latest.json`), and move the
   previous set into `superseded`. Run `tools/check-deployments.sh`.
2. Update the GitHub repository variables `MARKETPLACE_ADDR`, `AUCTION_ADDR`,
   `OFFERBOOK_ADDR`, `MARKETPLACE_MANAGER_ADDR`, `NFT_ADDR`, `INDEX_FROM_BLOCK`
   (CI refuses to deploy if they disagree with the JSON).
3. Wipe the Coston2 database's on-chain tables — the startup guard will otherwise refuse
   the new addresses: `POSTGRES_URL=… go run ./cmd/chainwipe` from `backend/`.
4. Enable offers on your test collection once, from the collection owner wallet:
   `cast send $OFFERBOOK "setOfferEligible(address,bool)" $NFT true --private-key …`
   (or use the "Enable offers" button on any token page of that collection while
   connected as the owner).
5. Push / merge to `main`; CI deploys; run one list → buy with two wallets.
