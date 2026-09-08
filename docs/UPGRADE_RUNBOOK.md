# Upgrade Runbook (v3.4+)

Every network — Coston2 (114), Songbird (19), Flare (14) — runs the same
bytecode, deployed UNSEALED: a single per-network **admin wallet** (saved
offline by the owner) holds upgrade + keeper-rotation rights until the owner
orders that network immutable.

**v3.4 upgrade model: INSTANT on every chain.** `upgradeDelay()` returns 0
everywhere (owner directive 2026-09-02). The 2-step queue survives for its
event trail, exact-implementation match, and `cancelUpgrade()` — not as a
delay. With no notice window, **custody of the admin key IS the entire
upgrade security model** for that network. Treat each admin key like the
network's root password: offline, never in the repo, never in Fly, never on
a hot machine longer than the minutes an upgrade takes.

## What is upgradeable

| Contract | Proxy? | How it changes |
|---|---|---|
| Marketplace / AuctionHouse / OfferBook | UUPS (ERC-1967) | `queueUpgrade` → `upgradeTo` (admin-gated via the manager) |
| MarketplaceManager | **NO — plain bytecode** | Never changes in place. To replace: deploy a new manager, build new core impls baking its address, upgrade the cores. |
| feeRecipient / manager address | impl **immutables** | New impl with new constructor args, installed via the same upgrade path |

## Performing an upgrade (per core, per network)

**One command (v3.6):** `tools/upgrade-cores.sh <network>` does everything
below for all three cores — deploys the implementations with the immutables
read from the live proxies, queues + installs each with `ADMIN_KEY`, verifies
`manager()`/`feeRecipient()` through the proxies, and records `impls` +
`superseded_impls` in `deployments/<network>.json`. `--dry-run` signs nothing,
`--verify` only prints the current state, `--rollback` reinstalls the recorded
previous implementations. Afterwards run `cd backend && go run ./cmd/reindexgov -from
<upgrade block>` (with the network's `CHAIN_ID`, `POSTGRES_URL`, `RPC_URL` and contract
addresses in the environment — the Go module lives in `backend/`) so
the `UpgradeQueued`/`Upgraded` events show on `/status` (the watcher normally
catches them live; the backfill is belt-and-braces). The manual steps:

```bash
# 0) Build and TEST the new implementation. forge test must be green.
cd contracts && forge build && forge test

# 1) Deploy the new implementation (constructor bakes feeRecipient+manager —
#    pass the SAME values unless the point of the upgrade is to change them):
forge create src/Marketplace.sol:Marketplace \
  --rpc-url <rpc> --private-key $DEPLOYER_KEY \
  --constructor-args $FEE_RECIPIENT $MANAGER_ADDR
# → note the new impl address $NEW_IMPL

# 2) Queue + install BACK-TO-BACK with the network's ADMIN key (instant —
#    upgradeDelay()==0). Use upgradeTo, NOT upgradeToAndCall(impl,"")
#    (OZ 4.9.6 force-calls empty calldata and reverts).
cast send $CORE_PROXY "queueUpgrade(address)" $NEW_IMPL --rpc-url <rpc> --private-key $ADMIN_KEY
cast send $CORE_PROXY "upgradeTo(address)"    $NEW_IMPL --rpc-url <rpc> --private-key $ADMIN_KEY

# 3) Verify through the proxy:
cast call $CORE_PROXY "feeRecipient()(address)" --rpc-url <rpc>
cast call $CORE_PROXY "manager()(address)"      --rpc-url <rpc>
cast call $CORE_PROXY "upgradeDelay()(uint64)"  --rpc-url <rpc>   # 0
```

Notes:
- A queued upgrade EXPIRES after 7 days (`MAX_UPGRADE_WINDOW`) and must be
  re-queued — an old approval cannot be exercised months later.
- The queue is one-shot and exact-match: only the queued impl installs, and
  installing consumes the entry.

## Compromised admin key — response

1. `cast send $CORE_PROXY "cancelUpgrade()" ...` on any core with a queued
   upgrade you did not queue (from the legitimate key, if still yours).
2. If the key itself is lost to an attacker, THEY can upgrade instantly:
   the only fail-safe is `renounceAdmin()` — if you can front-run with the
   still-valid key, sealing the network permanently removes the upgrade
   surface (and keeper rotation) before the attacker uses it. This destroys
   upgradeability forever; prefer it over letting an attacker hold it.
3. Rotate the keeper (`setKeeper`) only via the admin; keeper compromise is
   benign by design (settlement to recorded parties only) — admin compromise
   is not.

## Keeper rotation

```bash
cd backend                                                          # the Go module root
go run ./cmd/keeperrotate -gen -out keeper-<network>.key            # new key
go run ./cmd/keeperrotate -derive keeper-<network>.key     # its address
go run ./cmd/keeperrotate -set -granter $ADMIN_KEY -to <new-addr> \
  -manager $MANAGER_ADDR -rpc <rpc>                                # setKeeper
fly secrets set -a magicwebb-<network> KEEPER_KEY=<hex>            # deploy it
go run ./cmd/keeperrotate -fund -granter <funder-key> -to <new-addr> \
  -wei 100000000000000000 -rpc <rpc>                               # ≥0.1 native
```

(The pre-v3.2 `-grant` mode called `addKeeper`, which no longer exists.)

## Rotating the admin key (before immutability)

The admin key is the weak link while a network is unsealed — but that risk
is **temporary and rotatable**, and it **ends permanently** at
`renounceAdmin()`. Rotation is a 2-step hand-off so a mistyped address can
never strand a network: nothing changes until the NEW key proves it can sign
by accepting. Until then the old key keeps every power and can cancel.

```bash
# (the `go run` alternatives below run from backend/, the Go module root)
# 1) Current admin offers the role (sets pendingAdmin only):
cast send $MANAGER_ADDR "transferAdmin(address)" $NEW_ADMIN_ADDR --rpc-url <rpc> --private-key $ADMIN_KEY
#    or: go run ./cmd/keeperrotate -transfer-admin -granter $ADMIN_KEY \
#          -to $NEW_ADMIN_ADDR -manager $MANAGER_ADDR -rpc <rpc>

cast call $MANAGER_ADDR "pendingAdmin()(address)" --rpc-url <rpc>   # == $NEW_ADMIN_ADDR

# 2) NEW admin accepts from its own key (from this block the old key is dead
#    on the manager AND on every core's queueUpgrade/upgradeTo):
cast send $MANAGER_ADDR "acceptAdmin()" --rpc-url <rpc> --private-key $NEW_ADMIN_KEY
#    or: go run ./cmd/keeperrotate -accept-admin -granter $NEW_ADMIN_KEY \
#          -manager $MANAGER_ADDR -rpc <rpc>

# 3) Verify, then destroy the OLD key material:
cast call $MANAGER_ADDR "admin()(address)"        --rpc-url <rpc>   # == $NEW_ADMIN_ADDR
cast call $MANAGER_ADDR "pendingAdmin()(address)" --rpc-url <rpc>   # 0x000...000

# Changed your mind before step 2? The current admin withdraws the offer:
cast send $MANAGER_ADDR "cancelAdminTransfer()" --rpc-url <rpc> --private-key $ADMIN_KEY
```

Selectors (`cast sig`): `transferAdmin(address)` = `0x75829def`,
`acceptAdmin()` = `0x0e18b681`, `cancelAdminTransfer()` = `0x9a387e70`.

Lifecycle: **deploy (admin-held) → any number of `transferAdmin`/`acceptAdmin`
rotations → `renounceAdmin()` (final; also wipes any pending offer)**. There is
no path back from renunciation and no path to a second admin. A pending offer
never holds power — `hasRole(DEFAULT_ADMIN_ROLE, pending)` is false until
accepted — so a compromised *offeree* key gains nothing; cancel and re-offer.

## Safe as admin (mainnet)

On Songbird and Flare (not deployed yet — `deployments/<network>.json` is `read-only`)
the admin MUST be a Gnosis Safe from block one (`contracts/script/DeploySafe.s.sol` creates it; `DeployV34.s.sol` takes its address as
`ADMIN_ADDR`; threshold ≥ 2, owners saved offline). An existing EOA-held network moves
to a Safe with the same 2-step hand-off: `transferAdmin(<safe>)` from the EOA, then
`acceptAdmin()` executed **as a Safe transaction** (Transaction Builder → contract
`MarketplaceManager`, method `acceptAdmin`, no arguments; collect the threshold
signatures; execute). `/api/v1/governance` then reports `admin_is_contract: true`
and `/status` labels the admin as a contract.

Every admin action becomes a Safe transaction carrying the calldata the `cast send`
lines above would have sent:

```bash
cast calldata "setKeeper(address)" $NEW_KEEPER          # to the manager
cast calldata "queueUpgrade(address)" $NEW_IMPL         # to the CORE proxy
cast calldata "upgradeTo(address)" $NEW_IMPL            # same proxy, after the queue tx executed
cast calldata "cancelUpgrade()"                         # to the core proxy
cast calldata "renounceAdmin()"                         # to the manager — final
```

Queue and install can be ONE Safe transaction: batch `queueUpgrade(impl)` and
`upgradeTo(impl)` to the same proxy in the Transaction Builder (MultiSend runs as a
delegatecall, so `msg.sender` stays the Safe, and with `upgradeDelay() == 0` the
`upgradeEta == block.timestamp` check passes in the same block — the unit tests do
both calls back-to-back). Two separate Safe transactions also work; then the 7-day
`MAX_UPGRADE_WINDOW` counts from the queue transaction's block timestamp. The
implementation deploy (`forge create`, deployer key, no admin power) is unchanged; only
the queue + install calls move into the Safe. Verify through the proxies exactly as in
"Performing an upgrade" step 3. Keeper rotation on a Safe-held network: `setKeeper` as a
Safe transaction, then the Fly secret + funding steps above unchanged.

## Going immutable (per network, owner's order only)

One transaction from that network's admin:

```bash
cast send $MANAGER_ADDR "renounceAdmin()" --rpc-url <rpc> --private-key $ADMIN_KEY
```

From that block, on that network, forever: no core upgrades (every core's
`_requireAdmin` probes this manager), no keeper rotation, no admin, and any
in-flight `transferAdmin` offer is wiped. Verify:

```bash
cast call $MANAGER_ADDR "admin()(address)" --rpc-url <rpc>   # 0x000...000
```

Then destroy the admin key material. Update `deployments/<network>.json`
note + docs to record the sealing block.
