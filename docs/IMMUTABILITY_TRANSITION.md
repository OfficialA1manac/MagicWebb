# Immutability model (v3.2)

**Rewritten 2026-08-31.** The previous version of this document prescribed a
self-replenishing keeper *fleet* — minimum two `KEEPER_ROLE` keys, with
`addKeeper` as the post-renunciation recovery path. That doctrine is
**explicitly overridden by owner decision (2026-08-31)**: the ability of a
keeper key to enroll further keepers was judged a vulnerability, not a safety
net. v3.2 deletes the fleet, `addKeeper`, `removeKeeper`, and the entire
AccessControl role registry.

## The model

One `MarketplaceManager` per network holds exactly two addresses:

| Slot | Power | Lifetime |
|---|---|---|
| `keeper` | Settle ended auctions to their recorded parties, sweep refunds to their owners, clean expired listings. Cannot move, redirect, or block funds. Cannot change the keeper set. | Forever (replaceable only by the admin, while one exists) |
| `admin` | `setKeeper`, instant UUPS upgrades (`upgradeDelay` 0), 2-step `transferAdmin`/`acceptAdmin` (`cancelAdminTransfer` aborts a pending handover), `renounceAdmin`. | Until `renounceAdmin()` — then nobody, forever |

The cores (Marketplace, AuctionHouse, OfferBook) consult the manager through
the same `hasRole(bytes32,address)` staticcall protocol as v3.1; the manager
answers from the two fixed slots. There is no grant path anywhere: no
function in the deployed system adds a settlement-authorized address.

## Two deployment modes, one bytecode

- **Unsealed -- EVERY network (owner directive 2026-09-02):** `DeployV34.s.sol`
  with an `ADMIN_ADDR` (a fresh, per-network wallet the owner saves offline).
  The admin can rotate the keeper and perform INSTANT upgrades -- v3.4 sets
  `upgradeDelay()` to 0 on every chain; queueUpgrade and upgradeTo run
  back-to-back. With no notice window, custody of the admin key IS the
  entire upgrade security model until the network is sealed. The admin key
  is rotatable before sealing: `transferAdmin(new)` then `acceptAdmin()` from
  the new wallet (2-step; `cancelAdminTransfer()` aborts).
- **Sealed -- the go-immutable switch:** when the owner gives the order for a
  network, its admin calls `MarketplaceManager.renounceAdmin()` -- one way,
  one transaction. From that block: no admin, no upgrades, no keeper
  rotation, no grants, fee recipient fixed. (`SEAL=true` at deploy time
  still exists for a deliberate sealed-from-block-one deployment and
  requires a Safe/contract fee recipient.)

## Keeper key policy

The keeper's private key exists in exactly ONE place: the network's Fly
secret (`KEEPER_KEY`), which is write-only. No file, no doc, no repo copy.
Only the keeper's **address** is recorded (for funding).

**Consequence, accepted by the owner:** on a sealed network a lost or
compromised keeper key has no remedy. There is no rotation and no recovery —
the standing plan is to deploy a fresh contract set and migrate. What is
actually lost is automation only:

- instant settlement (seller/winner can always settle their own auction);
- automated loser refunds (`refundLosers` stays permissionless post-settle;
  `withdrawLoserFunds`/`withdrawRefund` stay self-service);
- third-party expired-offer refunds (bidders can always reclaim their own);
- expired-listing cleanup (`cleanExpired` — sellers can still `cancel()`
  their own listings at any time).

A compromised keeper key gains nothing: every keeper action pays the
recorded parties and only them, and the key cannot enroll other keepers.

**Funds are never trapped by keeper absence.** The one path that narrows in
v3.2: `forceCancel` (endsAt + 3 days) is keeper/seller/winner-only — the
trapped leader rescues themselves; outsiders cannot touch someone else's
auction.

## What is permanently foreclosed on sealed networks

- Bug-fix upgrades. A logic bug is permanent; the remedy is a fresh
  deployment and migration.
- Fee-recipient changes. Choose the Safe carefully.
- The token / fee-distributor / staking / governance module slots from
  v3.1 (`docs/TOKEN_HOOKS.md`) — those were admin-gated and are deleted;
  any future token architecture is a new contract generation.
