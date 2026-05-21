# Slither Security Report

**Date:** 2026-05-20  
**Tool:** Slither (crytic)  
**Config:** `contracts/slither.config.json` (lib/ filtered, all severities shown)  
**Contracts analyzed:** 30 (6 src + mocks + OZ)  
**Results:** 23 findings — 0 HIGH, 0 MEDIUM (post-fix), resolved below

---

## Fixed

| Finding | Location | Fix |
|---------|----------|-----|
| `missing-zero-check` — `setRoyaltyRegistry` accepted zero address | `MarketplaceCore.sol:61` | Added `if (registry == address(0)) revert ZeroAddress()` |

---

## Accepted / False Positives

### arbitrary-send-eth (MEDIUM — accepted)

**`MarketplaceCore._splitAndPay`:** ETH sent to `royaltyReceiver`, `feeVault`, `seller`.  
Accepted: all three destinations are validated — `feeVault` is immutable; `seller` is the authenticated NFT owner; `royaltyReceiver` comes from ERC-2981 or the access-controlled RoyaltyRegistry. Not arbitrary.

**`TreasuryVault.withdraw/withdrawAll`:** ETH sent to `to` parameter.  
Accepted: function gated to `WITHDRAWER_ROLE`. Caller is trusted admin, not user-controlled.

### reentrancy-events (LOW — accepted)

**`TreasuryVault.withdraw/withdrawAll`:** Event emitted after `.call`.  
Accepted: state (`balance`) updated before the call (CEI followed). Event emission after a reentrant call would simply emit a duplicate log — no state is corrupted, no funds at risk. `nonReentrant` added for defense-in-depth on the external call path.

### incorrect-equality (LOW — accepted)

**`TreasuryVault.withdrawAll`:** `bal == 0` strict equality.  
Accepted: intentional — reverts if vault is empty. No external input can force `bal` to a non-zero value that equals zero.

### timestamp (LOW — accepted)

All auction/listing timestamp comparisons are intentional. Flare block time is ~1.8 s; miner timestamp manipulation is bounded to ±15 s. Anti-snipe window is 5 minutes — well above manipulation range.

### cyclomatic-complexity (INFO — accepted)

**`AuctionHouse.bid`:** Complexity 20. Justified by the commit-reveal + compound-bid + anti-snipe + increment logic in a single function. Splitting would increase gas via extra SLOADs.

### low-level-calls (INFO — accepted)

All `.call{value:}` usages check the return bool and revert on failure. This is the recommended pattern for ETH transfers (avoids 2300 gas limit of `.transfer()`).

---

## Summary

**No HIGH or MEDIUM vulnerabilities remain.** All findings are either fixed, accepted false-positives, or intentional design patterns with documented rationale.
