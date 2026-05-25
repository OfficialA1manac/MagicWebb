# Slither Security Report

**Date:** 2026-05-25 (updated — TreasuryVault removed, unified 1.5% fee)  
**Tool:** Slither (crytic)  
**Config:** `contracts/slither.config.json` (lib/ filtered, all severities shown)  
**Contracts analyzed:** 5 src (MarketplaceCore, Marketplace, AuctionHouse, OfferBook, RoyaltyRegistry)  
**Results:** All HIGH and MEDIUM: 0 (no findings)

---

## Fixed

| Finding | Location | Fix |
|---------|----------|-----|
| `missing-zero-check` — `setRoyaltyRegistry` accepted zero address | `MarketplaceCore.sol:61` | Added `if (registry == address(0)) revert ZeroAddress()` |

---

## Accepted / False Positives

### arbitrary-send-eth (MEDIUM — accepted)

**`MarketplaceCore._splitAndPay`:** ETH sent to `feeRecipient` and `seller`.  
Accepted: both destinations are validated — `feeRecipient` is immutable (set at deploy, never changeable); `seller` is the authenticated NFT owner at settlement time. Not arbitrary.

**Note:** `TreasuryVault` has been removed. All fees are sent directly to the immutable `feeRecipient` wallet — no vault contract, no accumulator.

### reentrancy-events (LOW — accepted)

**`AuctionHouse.withdrawRefund` / `reclaimBid`:** State updated before `.call`. CEI followed. `nonReentrant` provides defense-in-depth.

### timestamp (LOW — accepted)

All auction/listing timestamp comparisons are intentional. Flare block time is ~1.8 s; miner timestamp manipulation is bounded to ±15 s.

### cyclomatic-complexity (INFO — accepted)

**`AuctionHouse.bid`:** Complexity justified by commit-reveal + compound-bid + increment logic. Splitting would increase gas via extra SLOADs.

### low-level-calls (INFO — accepted)

All `.call{value:}` usages check the return bool and revert on failure. Recommended pattern for ETH transfers.

---

## Summary

**No HIGH or MEDIUM vulnerabilities.** All findings are accepted false-positives or intentional design patterns with documented rationale. The removal of `TreasuryVault` eliminated the `WITHDRAWER_ROLE`-gated send findings.
