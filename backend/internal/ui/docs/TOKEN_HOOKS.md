# Token Integration Points

Status: **architecture slots only — no token logic is implemented or deployed.**
Anchor contract: `contracts/src/MarketplaceManager.sol`.

## Where the hooks live

| Hook | Location | Set by | Purpose when a native token ships |
|------|----------|--------|-----------------------------------|
| `setTokenAddress(address)` | `MarketplaceManager.sol` | `DEFAULT_ADMIN_ROLE` | Registers the marketplace token. Single discovery point for every other module and for the backend indexer. |
| `setFeeDistributor(address)` | `MarketplaceManager.sol` | `DEFAULT_ADMIN_ROLE` | Token-based fee rebates. The core 1.5 % fee is immutable and always flows to `feeRecipient`; a FeeDistributor would sit **behind** that address (deploy the distributor, point `feeRecipient` of the *next* core version at it, or have the fee wallet forward). `FEE_MANAGER_ROLE` is pre-defined for its operators. |
| `setStakingModule(address)` | `MarketplaceManager.sol` | `DEFAULT_ADMIN_ROLE` | Token utility (e.g. staked-tier perks). Reads `manager.token()`; cores stay untouched. |
| `setGovernanceModule(address)` | `MarketplaceManager.sol` | `DEFAULT_ADMIN_ROLE` | On-chain governance. Recommended end-state: transfer `DEFAULT_ADMIN_ROLE` to this module (or a timelock in front of it). |

## Invariants any future token module MUST respect

1. **Cores are immutable.** Token features integrate by *reading* the manager registry, never by modifying Marketplace/AuctionHouse/OfferBook. New behavior requiring core changes = new core version + `setCoreContracts` re-point.
2. **Exits stay unstoppable.** No module may sit between a user and `settle` / `refundLosers` / `withdrawRefund` / `rejectOffer` / `refundExpiredOffer` / `cancel*`.
3. **The 1.5 % fee is fixed.** Rebates are paid from collected fees by the FeeDistributor, never by changing `PLATFORM_FEE_BPS`.
4. **The manager holds no funds** (no payable surface — enforced by test `test_managerHoldsNoFundsPath`).

## Off-chain integration points

- `MANAGER_ADDR` is exported by both deploy scripts; add to `backend/.env` when the backend needs role/pause introspection (e.g. surfacing "marketplace paused" in the UI via `entriesAllowed()`).
- Indexer: `ModuleSet` / `AuditLog` / `EntriesPaused` events are uniform and indexed — extend `coreTopics()` in `backend/internal/indexer/abis.go` when token modules ship.
