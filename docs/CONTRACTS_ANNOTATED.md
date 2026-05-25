# MagicWebb Smart Contracts

All contracts inherit `MarketplaceCore` which provides: `feeRecipient` (immutable wallet address), `PLATFORM_FEE_BPS` (constant 150 = 1.5%), `pause`/`unpause`, `AccessControl`, `ReentrancyGuard`.

---

## Marketplace

Fixed-price ERC-721 and ERC-1155 listings.

### Key functions

| Function | Who | Description |
|----------|-----|-------------|
| `list(coll, id, price, expiresAt)` | seller | List one ERC-721. Requires ownership + approval. 1.5% listing fee paid upfront. |
| `list1155(coll, id, amount, price, expiresAt)` | seller | List ERC-1155 tokens. 1.5% listing fee paid upfront. |
| `batchList(items[])` | seller | List up to 50 ERC-721 tokens in one tx. Each item: `{coll, id, price, expiresAt}`. Reverts if `items.length == 0 || items.length > 50` (`BatchTooLarge`). 1.5% listing fee on each item, summed. |
| `cancel(coll, id)` | seller | Remove listing. Works while paused. |
| `buy(coll, id)` | buyer | Buy at exact listing price. NFT → buyer, 1.5% fee → feeRecipient wallet, remainder → seller. Atomic — entire tx reverts if NFT transfer fails. |

### Listing struct (2 storage slots)
```
slot 0: seller(20) + expiresAt(8) + standard(1)
slot 1: price(16) + amount(16)
```

---

## AuctionHouse

English auctions with fixed end time, reserve price, min bid increment, and commit-reveal MEV protection.

### Constants
| Constant | Value | Purpose |
|----------|-------|---------|
| `MAX_MIN_INCREMENT_BPS` | 5000 (50%) | Prevents absurd min increment griefing |
| `SETTLE_DEADLINE` | 7 days | After this past `endsAt`, winner may reclaim bid |
| `COMMIT_DELAY_BLOCKS` | 2 | Min blocks between commit and reveal |

**No anti-snipe.** `endsAt` is immutable after creation. Bids in the final seconds do not extend the clock.

### Auction struct (6 storage slots, 12 fields)
```
slot 0: seller(20) + startsAt(8) + minIncrementBps(2) + settled(1) + standard(1)
slot 1: collection(20) + endsAt(8)
slot 2: tokenId(32)
slot 3: reserve(16) + highestBid(16)
slot 4: highestBidder(20)
slot 5: amount(16)
```

### Bid flow (commit-reveal)
```
1. commitBid(id, keccak256(abi.encode(id, bidder, fullAmount, salt)))
   — stores hash on-chain, emits BidCommitted
2. Wait COMMIT_DELAY_BLOCKS (2 blocks ≈ 3.6 s on Flare)
3. bid(id, fullAmount, salt) with msg.value = fullAmount (new bidder)
                                            or fullAmount - prevHighBid (rebidder)
   — verifies hash, enforces delay, updates highestBid/Bidder
   — previous high bidder's ETH queued in pendingReturns
```

### Key functions
| Function | Who | Description |
|----------|-----|-------------|
| `create(coll, id, reserve, startsAt, endsAt, minIncBps)` | seller | Create ERC-721 auction. |
| `create1155(...)` | seller | Create ERC-1155 auction. |
| `commitBid(id, commitment)` | bidder | Phase 1: store bid commitment. |
| `bid(id, fullAmount, salt)` | bidder | Phase 2: reveal and apply bid. |
| `settle(id)` | anyone | After `endsAt`: transfers NFT to winner, 1.5% fee → feeRecipient wallet, remainder → seller. Called automatically by keeper bot. |
| `cancel(id)` | seller | Cancel if no bids exist. |
| `withdrawRefund()` | outbid bidder | Claim accumulated refunds from pendingReturns. |
| `reclaimBid(id)` | winner | If `settle` not called within 7 days of `endsAt`, winner reclaims ETH. |

---

## OfferBook

Off-chain EIP-712 signed offers with on-chain ETH escrow.

### Key functions
| Function | Who | Description |
|----------|-----|-------------|
| `deposit()` | offeror | Deposit ETH to use across all offers. |
| `withdraw(amount)` | offeror | Withdraw deposited ETH. |
| `acceptOffer(offer, sig)` | token owner | Accept a signed offer: verifies EIP-712 sig, transfers NFT, 1.5% fee → feeRecipient wallet, remainder → seller. |
| `cancelOffer(offer)` | offeror | Invalidate an offer. |

---

## MarketplaceCore (base)

| Item | Value |
|------|-------|
| `feeRecipient` | Immutable wallet address. Set at deploy. Receives all platform fees directly. |
| `PLATFORM_FEE_BPS` | Constant `150` (1.5%). Hardcoded — cannot be changed by any admin or env var. |
| `_splitAndPay(seller, amount)` | Deducts `PLATFORM_FEE_BPS/10000 * amount` → feeRecipient, remainder → seller. |
| `_transferToken(std, coll, from, to, id, amt)` | Handles ERC-721 and ERC-1155 transfers. |

Fee formula: `fee = amount * 150 / 10_000`. Seller receives `amount - fee`. Fee is sent via `.call{value: fee}("")` directly to the `feeRecipient` wallet.
