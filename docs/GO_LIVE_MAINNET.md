# Go-live: Songbird and Flare (wave 9)

Everything below the "Owner steps" line is already done. Songbird and Flare run
the same image as Coston2 in read-only mode today; flipping either one to
trading is: fund → deploy the contracts → record the addresses → push. Both
networks stay **admin-held and instantly upgradeable** (`upgradeDelay() == 0`;
v3.7: the `MarketplaceManager` is a UUPS proxy too, so all four contracts can be
changed in place) until the owner orders `renounceAdmin()` — see `UPGRADE_RUNBOOK.md`.

## Already in place (verified 2026-09-08)

| Item | Songbird (19) | Flare (14) |
|---|---|---|
| Fly app, read-only, healthy | `magicwebb-songbird.fly.dev` `/readyz` 200 | `magicwebb-flare.fly.dev` `/readyz` 200 |
| Neon Postgres (Singapore) | `bitter-pond-73256956` | `royal-paper-82216877` |
| Fly secrets | `POSTGRES_URL` `JWT_SECRET` `WC_PROJECT_ID` + `KEEPER_KEY` (staged; applies on the next deploy) | same |
| Keeper wallet (address to fund) | `0x979Dd049B9C7952f768e753bae575c199f847E4c` | `0x1F0aD251f923579781EcB7D8D76edE08a6A498FF` |
| Keeper key backup | the local file `keeperrotate -gen -out` wrote → move it to the password manager, then delete it | same |
| CI deploy flag | `SONGBIRD_ENABLED=true` | `FLARE_ENABLED=true` |
| RPC primary + fallbacks answer `eth_chainId` | flare-api + ankr | flare-api + ankr + thirdweb |
| Safe v1.3.0 singleton + proxy factory on-chain | eip155 addresses, bytecode present | canonical addresses, bytecode present |
| Keeper gas caps vs live base fee | 2000 / 200 gwei vs 500 gwei base (was 200 — would have starved) | same |
| Deploy record helper | `python tools/record-deployment.py songbird` | `… flare` |
| Explorer source verification | `deploy.yml` runs `forge verify-contract` (Blockscout); non-fatal | same |

Deployer (the only key that pays for the deploy): **`0x14080c66253dfc5042ad5226c7d8730f8bc69f91`**
— balance 0 on both networks today.

## One-command path (2026-09-10)

The owner's only action is **one transfer per network to the deployer
`0x14080c66253dfc5042ad5226c7d8730f8bc69f91`: 72 SGB on Songbird** (22 for the
8-CREATE deploy — measured 19.46 FLR on Flare on 2026-09-11 at a 500 gwei base
fee — + 50 that the script forwards to that network's keeper). Flare went live
on 2026-09-11 (block 69547144) from a 63 FLR top-up: 19.46 deploy, 40 to the
keeper, the remainder returned to the funding wallet. Then:

```bash
tools/go-live.sh songbird          # preflight → DeployV34 → record → check → fund keeper → commit → push (= deploy)
tools/go-live.sh flare
tools/go-live.sh songbird --dry-run   # simulate only; safe before funding
```

Defaults the script uses (override with env vars): admin =
`0x987f10f49b35a8ef48b664a8dc457f61c6fe2105` (the same offline wallet that
is the Coston2 admin, so one wallet holds instant-upgrade rights on all three
networks until you order `renounceAdmin()`), fee recipient =
`0x78993B71051de91C2D2595BC3475F07748927dc0`, keeper = the per-network
wallet in the table above, deployer key = the repo-root `.env` (gitignored,
never printed). To use a Safe as admin instead, deploy it first (step 1
below) and pass `ADMIN_ADDR=<safe>`.

## Manual steps (the same procedure by hand; per network, Songbird first)

**0. Fund** (base fee 500 gwei, priority 150 gwei, deploy = 8 CREATEs ≈ 13M gas —
v3.7 adds the manager's own impl + proxy so every contract is upgradeable):

| Send to | Songbird | Flare | Why |
|---|---|---|---|
| Deployer `0x14080c66…9f91` | 15 SGB | 15 FLR | contracts (≈ 8 native) + Safe (≈ 0.3) + headroom |
| Keeper `0x979Dd049…7E4c` | 50 SGB | — | settle / refund / clean transactions (≈ 0.1 native each at 650 gwei) |
| Keeper `0x1F0aD251…98FF` | — | 50 FLR | same |

**1. Decide the admin and the fee recipient.** Either give 2–3 Safe owner
addresses + threshold (recommended, plan decision "Safe 2-of-3") or name a fresh
admin EOA you keep offline. Fee recipient: a Safe (recommended) or the Coston2
default `0x78993B71051de91C2D2595BC3475F07748927dc0`.

```bash
# Safe (skip if the admin is an EOA). Prints the Safe address = ADMIN_ADDR.
cd contracts
SAFE_OWNERS="0xA…,0xB…,0xC…" SAFE_THRESHOLD=2 \
  forge script script/DeploySafe.s.sol --rpc-url https://songbird-api.flare.network/ext/C/rpc \
  --broadcast --private-key $PRIVATE_KEY
```

**2. Deploy the contracts** (unsealed: no `SEAL`, so upgrades stay instant):

```bash
cd contracts
PRIVATE_KEY=0x… ADMIN_ADDR=0x… FEE_RECIPIENT_ADDR=0x… \
KEEPER_ADDR=0x979Dd049B9C7952f768e753bae575c199f847E4c \
  forge script script/DeployV34.s.sol --rpc-url https://songbird-api.flare.network/ext/C/rpc --broadcast -vv
# Flare: KEEPER_ADDR=0x1F0aD251f923579781EcB7D8D76edE08a6A498FF, rpc https://flare-api.flare.network/ext/C/rpc
```

The script asserts on-chain that `feeRecipient`, `manager` and `keeper()` are
wired before it exits.

**3. Record and push** (this is the whole enable procedure — CI redeploys the
app with the addresses, the staged `KEEPER_KEY` applies, indexer + keepers start):

```bash
python tools/record-deployment.py songbird      # fills deployments/songbird.json from the broadcast
bash tools/check-deployments.sh
git add deployments/songbird.json && git commit -m "songbird: contracts live at block <n>" && git push
```

**4. Verify live** (≈ 10 minutes after the push):

```bash
curl -sI https://magicwebb-songbird.fly.dev/healthz | grep -i x-mw-build-sha
curl -s https://magicwebb-songbird.fly.dev/readyz
curl -s https://magicwebb-songbird.fly.dev/api/v1/governance   # deployed:true, admin, keeper, upgrade_delay 0
curl -s https://magicwebb-songbird.fly.dev/api/v1/indexer/slo   # head_lag_blocks < 30
bash tools/check-governance.sh
```

`/status` → "Admin & upgrades" shows the deploy-time `KeeperSet`. If the trail
is empty: `cd backend && CHAIN_ID=19 POSTGRES_URL=… RPC_URL=… MARKETPLACE_ADDR=…
AUCTION_ADDR=… OFFERBOOK_ADDR=… MARKETPLACE_MANAGER_ADDR=… go run ./cmd/reindexgov -from <deploy block>`.
Then one real list → buy round-trip between two wallets.

**5. Collections.** Mainnets launch with no seed collection (`SeedCollection.s.sol`
is testnet-only) and an empty `trackedCollections`; any collection becomes
tracked the first time it is listed, auctioned or offered on, and the verifier
badges it from on-chain facts. To pre-track known collections:
`python tools/record-deployment.py songbird --track 0x…,0x…` (or edit the JSON).

**6. Later, on your order only:** `renounceAdmin()` per network seals it
forever (`UPGRADE_RUNBOOK.md` → "Going immutable"). Until then the admin can
`tools/upgrade-cores.sh <network>` instantly.

## What is not done and why

- Nothing that needs the deployer key or funds: the deploy itself, the Safe,
  the record + push. No mainnet balance exists yet.
- Neon daily snapshot schedule on the three Singapore projects: changes
  billing, owner-gated (`RUNBOOK_RESTORE.md`).
- Deleting the idle EU Neon projects (`snowy-mountain-21008952`,
  `falling-dust-05670744`): destructive, needs a separate OK.
- The Coston2 core upgrade to the 15-duration implementations
  (`tools/upgrade-cores.sh coston2`): needs the Coston2 admin key.
