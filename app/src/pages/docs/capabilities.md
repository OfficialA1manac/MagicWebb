---
layout: ../../layouts/DocLayout.astro
title: "What You Can Do"
description: "Every action for viewers, buyers and sellers — listings, auctions, offers, search, refunds."
---


MagicWebb has no accounts and no approval step. The trading contracts are immutable and no
role can pause, censor or reprice a trade; a separate MarketplaceManager contract holds
narrow operational roles (keeper authorization for auto-settling auctions, module registry)
that cannot touch trading actions or escrowed funds. Three kinds of visitor exist, and
a person is all three at different moments:

- **Viewer** — no wallet connected.
- **Buyer** — wallet connected on this network, does not own the NFT in question.
- **Seller / owner** — wallet connected, owns the NFT (or created the listing/auction).

Every write is a wallet transaction on the network you are viewing. The marketplace fee is
**2% (1.5% platform + 0.5% keeper gas fund), paid by the seller, only when something sells**. Listing, auctioning, bidding and
offering are free (gas only). Escrow you put up stays withdrawable until it becomes a sale:
winning bids and accepted offers pay the seller, while outbid, cancelled, declined, expired
and failed-payout amounts are all refundable.

## Which networks?

| Capability | Coston2 | Songbird | Flare |
|---|---|---|---|
| Browse, search, collections, activity, docs | ✓ | ✓ | ✓ |
| Connect wallet, view profile + balance | ✓ | ✓ | ✓ |
| Trade (list, buy, auction, bid, offer, refunds) | ✓ | after audit | after audit |

Songbird and Flare are **read-only** until the contracts pass the external security audit.
Trade there once it ships; until then, trade on Flare Coston2. Switching network navigates
to a different site, so you connect your wallet again on arrival.

## Listings (fixed price)

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| Browse, filter, sort, search, see price history | ✓ | ✓ | ✓ |
| See the Verified NFT badge and what it means | ✓ | ✓ | ✓ |
| Buy (pays the price; receives the NFT in the same tx) | connect first | ✓ | not your own |
| List an NFT (price ≥ 1 native, duration 1m–24h (14 fixed durations)) | — | only NFTs you own | ✓ |
| Change price | — | — | ✓ own listing |
| Cancel listing (NFT never left your wallet) | — | — | ✓ own listing |
| Batch-list up to 50 | — | — | ✓ |
| Save a search / get notifications | — | ✓ (sign-in message) | ✓ |

## Auctions

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| Watch live countdown, bids, anti-snipe extensions | ✓ | ✓ | ✓ |
| Create auction (reserve ≥ 1 native, duration 1m–24h (14 fixed durations)) | — | — | ✓ |
| Bid — bids are cumulative; your total must beat the leader by ≥ 1 native | — | ✓ | not your own |
| Be outbid → withdraw your total any time | — | ✓ | — |
| Settle after it ends (NFT → winner, proceeds → seller minus 2%) | — | ✓ if you won it | ✓ |
| Cancel early | — | — | ✓ only while there are no bids |
| Last 3 minutes: every bid extends the end by 3 min (max 30 min total) | rule | rule | rule |

**Who can settle.** Three parties, and no one else: the marketplace's own keeper (which
settles the moment the auction ends, so normally nobody has to do anything), the seller, and
the winner. A passer-by cannot settle someone else's auction. This never traps money — if
you did not win, your escrow is withdrawable at any time, and if an auction is somehow left
unsettled for three days anyone may force-cancel it and return every bid.

**Bids must take the lead.** A bid that clears neither the reserve nor the leader's total
plus the flat 1-native-token increment is rejected outright rather than parked as escrow — you never
end up with money locked behind a position that cannot win.

## Offers (escrowed)

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| See open offers on any NFT | ✓ | ✓ | ✓ |
| Make an offer (amount ≥ 1 native escrowed, duration 1m–24h (14 fixed durations)) | — | ✓ if the collection allows offers | not on your own |
| Raise / lower your offer (keeps original expiry) | — | ✓ | — |
| Cancel your offer → full refund | — | ✓ before expiry | — |
| Accept an offer (NFT → bidder, funds → you minus 2%) | — | — | ✓ owner |
| Decline an offer (bidder refunded in full) | — | — | ✓ owner |
| Refund an expired offer (your own always; keeper sweeps others) | — | ✓ | ✓ |
| Enable offers for a collection | — | — | ✓ collection **contract** owner (ERC-173) |

## Search & discovery

Everyone: full-text search over NFTs and collections, trending, collection pages with traits,
activity feed, live updates over WebSocket. No results are hidden from viewers.

Long lists — your NFTs, listings, auctions and offers — are paged rather than truncated, so
a large wallet shows everything it holds.

A token page works even for an NFT the indexer has never seen: if the marketplace has no
record of it, the page reads the collection straight from the chain and renders what it
finds. Newly minted or just-transferred NFTs are therefore viewable immediately.

## Your profile

Connect a wallet and you can set a display name, a short bio, links, and a **tag** — a
label of your own choosing that replaces the automatically derived collector badge.

Profiles are shared across networks. What you set on one network carries over to the others,
so you fill it in once rather than once per network. Your NFTs, listings and auctions remain
per-network, because those live in each network's own contracts.

## Refunds

Outbid? Offer declined? Payment could not be pushed to your wallet? The amount is held for
you in the contract. **Profile → "Refunds waiting for you" → Withdraw.** Never expires.

## What nobody can do

- Freeze, move or take your NFT or funds (non-custodial; contracts hold only what you escrow).
- Curate or approve listings — the Verified NFT badge is computed from on-chain facts.
- Change the fee.
- Pause anything administratively. There is no circuit breaker, no operator role, and no
  entry gate — no role can halt listings, bids, offers, buys, cancels, settlements, or
  withdrawals. Normal contract rules (ownership, expiry, authorization, escrow) still
  apply to every action.
- Log in to an admin page. There is none.

## Why durations instead of dates?

The contracts accept exactly fourteen durations (1m, 3m, 5m, 15m, 30m, 45m, 1h, 2h,
4h, 8h, 12h, 16h, 20h, 24h) and compute the expiry from the block that mines
your transaction. A date picker cannot satisfy that from a wallet (you do not know the
mining block's timestamp), so every time-bound action asks for a duration.
