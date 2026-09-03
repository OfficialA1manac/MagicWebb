# Token integration hooks

> **FORECLOSED in v3.2 (2026-08-31).** The four module slots and their
> admin-gated setters were DELETED from `MarketplaceManager` along with the
> entire AccessControl role registry (owner decision: single fixed keeper, no
> role granting; every network deploys unsealed and is sealed later by an
> explicit per-network `renounceAdmin()`). On a sealed network there is no
> admin to call any setter, so the slot architecture below cannot exist there
> even in principle. A future token / fee-distributor / staking / governance
> rollout requires a NEW contract generation and user migration. This document
> is retained as the design record of what such a generation would wire up.

`MarketplaceManager` (v3.1 — removed in v3.2) reserved four address slots for a future native token and the
modules that would surround it. This document exists because the contract points at
it twice (`MarketplaceManager.sol:32` and `:93`) and because the slots are easy to
misread as live wiring.

**They are not wiring. Today they are inert storage.**

## What exists

| Slot | Setter | Intended future role |
|------|--------|----------------------|
| `token` | `setTokenAddress(address)` | native marketplace token |
| `feeDistributor` | `setFeeDistributor(address)` | token-based rebate of the platform fee |
| `stakingModule` | `setStakingModule(address)` | token utility (staking / boosts) |
| `governanceModule` | `setGovernanceModule(address)` | on-chain governance |

Each setter is `onlyRole(DEFAULT_ADMIN_ROLE)`, runs the address through
`_validContract` (must be a contract, not an EOA), writes the slot, and emits
`ModuleSet` + `AuditLog`.

## What they do not do

Nothing reads them. Grep the contract sources: `feeDistributor`, `stakingModule`,
and `governanceModule` appear **only** in their own declarations and setters, and
`token` never appears in `Marketplace.sol`, `AuctionHouse.sol`, `OfferBook.sol`, or
`MarketplaceCore.sol` outside of unrelated ERC-721/ERC-1155 imports. Setting any of
them changes no behaviour anywhere in the deployed system — it writes a slot and
emits an event that off-chain tooling can watch.

In particular:

- The 1.5% platform fee is **immutable and hard-coded in the cores**. A
  `feeDistributor` address does not, and cannot, redirect or rebate it. Fee
  changes would require a core upgrade through the admin-gated upgrade path
  (instant — no timelock), not a slot write.
- No sale, bid, offer, or settlement path consults any of these slots, so a
  misconfigured or hostile address in one of them cannot halt a user action or
  touch escrowed funds. This is the same guarantee the manager itself carries:
  "pausable entries, unstoppable exits" is unaffected by these four slots.

## Interaction with immutability

The end state for this deployment is full immutability via admin renunciation
(see [IMMUTABILITY_TRANSITION.md](IMMUTABILITY_TRANSITION.md)).

`DEFAULT_ADMIN_ROLE` gates all four setters, so **once the admin role is renounced
these slots become permanently unwritable.** Whatever they hold at that moment —
`address(0)` if never set — is what they hold forever. Any token programme that
depends on them must therefore either:

1. set the slots before renunciation, accepting that the modules behind them are
   still inert until a core upgrade teaches the cores to read them (and core
   upgrades are instant while an admin exists, and gone after renunciation); or
2. treat the slots as abandoned and build the token programme in a separate
   contract system that does not need the manager's permission.

Option 2 is the realistic path after renunciation. Nothing about the marketplace's
core behaviour depends on either choice.

## Why keep them

They cost four storage slots and nothing else. Removing them from an upgradeable
contract would shift storage layout, which is a strictly worse trade than leaving
four unread addresses in place.
