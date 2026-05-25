# MagicWebb Smart Contracts

All contracts inherit `MarketplaceCore` which provides: `feeRecipient` (immutable wallet address), `PLATFORM_FEE_BPS` (constant 150 = 1.5%), `ReentrancyGuard`. No pause, no admin role — contracts run forever once deployed.

No royalties are supported or enforced by any contract.

---

## Marketplace

Fixed-price ERC-721 and ERC-1155 listings.

### Key functions

| Function | Who | Description |
|----------|-----|-------------|
| `list(coll, id, price, expiresAt)` | seller | List one ERC-721. Requires ownership + approval. 1.5% listing fee paid upfront. |
| `list1155(coll, id, amount, price, expiresAt)` | seller | List ERC-1155 tokens. 1.5% listing fee paid upfront. |
| `batchList(items[])` | seller | List up to 50 ERC-721 tokens in one tx. Each item: `{coll, id, price, expiresAt}`. Reverts if `items.length == 0 || items.length > 50` (`BatchTooLarge`). 1.5% listing fee on each item, summed. |
| `cancel(coll, id)` | seller | Remove listing. |
| `buy(coll, id)` | buyer | Buy at exact listing price. NFT → buyer, 1.5% fee → feeRecipient wallet, remainder → seller. Atomic — entire tx reverts if NFT transfer fails. |

### Listing struct (2 storage slots)
```
slot 0: seller(20) + expiresAt(8) + standard(1)
slot 1: price(16) + amount(16)
```

---

## AuctionHouse

English auctions with auto-settlement, single-step bidding, and automatic push-refunds for outbid bidders.

### Constants
| Constant | Value | Purpose |
|----------|-------|---------|
| `MAX_MIN_INCREMENT_BPS` | 5000 (50%) | Prevents absurd min increment griefing |
| `NO_BID_CANCEL_WINDOW` | 30 minutes | Auction auto-cancels if no bid within this window |

**No anti-snipe.** `endsAt` is immutable after creation.

### Auction struct (6 storage slots, 13 fields)
```
slot 0: seller(20) + startsAt(8) + minIncrementBps(2) + settled(1) + standard(1)
slot 1: collection(20) + endsAt(8)
slot 2: tokenId(32)
slot 3: reserve(16) + highestBid(16)
slot 4: highestBidder(20)
slot 5: amount(16) + highestTotal(16)
```

`highestBid` = the bid amount proper (used for reserve/increment checks).
`highestTotal` = exact ETH held for highest bidder (bid + 1.5% fee). Used for push-refunds and settlement.

### Bid flow (single-step)
```
1. bid(id, bidAmount) with msg.value = bidAmount + floor(bidAmount * 150 / 10000)
   — validates bid meets reserve/increment
   — records new highestBid + highestTotal
   — previous high bidder's ETH (highestTotal) is pushed back immediately
   — BidPlaced event emitted
```

### Settlement
At or after `endsAt`, anyone calls `settle(id)` (keeper bot does this automatically):
```
- NFT transferred: seller → winner
- Fee = highestTotal - highestBid  (exact premium paid by winner)
- feeRecipient receives: fee
- seller receives: highestBid (full bid amount)
```

### Key functions
| Function | Who | Description |
|----------|-----|-------------|
| `create(coll, id, reserve, endsAt, minIncBps)` | seller | Create ERC-721 auction. Starts immediately. |
| `create1155(...)` | seller | Create ERC-1155 auction. |
| `bid(id, bidAmount)` | bidder | Single-step bid. msg.value = bidAmount + 1.5% fee. Outbid refund pushed automatically. |
| `settle(id)` | anyone | After `endsAt`: NFT → winner, fee → feeRecipient, bid → seller. Keeper calls automatically. |
| `cancelIfInactive(id)` | anyone | Cancel zero-bid auction after 30-minute window. Keeper calls automatically. |
| `cancelEarly(id)` | seller only | Seller cancels before `endsAt`. Refunds highest bidder if any. Requires manual tx approval. |
| `withdrawRefund()` | bidder | Emergency: reclaim ETH if automatic push-refund failed (edge case). |

---

## OfferBook

On-chain NFT offer system. Owners opt tokens in to receive offers.

### State
```solidity
mapping(address => mapping(uint256 => address)) public eligible;       // ERC-721 eligibility
mapping(address => mapping(uint256 => address)) public eligible1155;   // ERC-1155 eligibility
mapping(address => mapping(uint256 => mapping(address => uint256))) public offers;        // ERC-721 offers (ETH)
mapping(address => mapping(uint256 => mapping(address => Offer1155))) public offers1155;  // ERC-1155 offers
```

### Offer flow
```
1. Owner: markEligible(coll, tokenId)        — signals willingness to receive offers
2. Bidder: makeOffer(coll, tokenId)          — deposits ETH offer on-chain
3. Owner: acceptOffer(coll, tokenId, bidder) — NFT → bidder, fee → feeRecipient, remainder → owner
   OR
   Bidder: withdrawOffer(coll, tokenId)      — reclaims full ETH, no fee taken
```

### Fee on offers
Fee is 1.5% of offer amount, deducted from seller proceeds at acceptance. Bidder gets full ETH back if offer not accepted — no fee.

### Key functions
| Function | Who | Description |
|----------|-----|-------------|
| `markEligible(coll, tokenId)` | token owner | Mark ERC-721 as eligible for offers. |
| `removeEligible(coll, tokenId)` | owner who marked | Stop receiving new offers. Existing offers persist. |
| `makeOffer(coll, tokenId)` | anyone | ETH offer for eligible ERC-721. Accumulates on repeat calls. |
| `withdrawOffer(coll, tokenId)` | offeror | Full ETH refund — no fee. |
| `acceptOffer(coll, tokenId, bidder)` | token owner | Accept offer. NFT → bidder. Eligibility auto-cleared. |
| `markEligible1155(coll, tokenId)` | holder | Mark ERC-1155 as eligible. |
| `removeEligible1155(coll, tokenId)` | owner who marked | Stop receiving ERC-1155 offers. |
| `makeOffer1155(coll, tokenId, units)` | anyone | One offer per bidder. Withdraw to update. |
| `withdrawOffer1155(coll, tokenId)` | offeror | Full ETH refund. |
| `acceptOffer1155(coll, tokenId, bidder)` | holder | Accept ERC-1155 offer. |
