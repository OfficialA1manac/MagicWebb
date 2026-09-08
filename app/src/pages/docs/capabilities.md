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

## What each role can do

| Action | Viewer | Buyer (wallet) | Seller/Owner |
|---|---|---|---|
| Browse, search, view token/collection/profile/activity | ✓ | ✓ | ✓ |
| Buy a listing | connect prompt | ✓ | hidden on own |
| List (721 / 1155 units), 15 durations, min 1 | — | — | ✓ owner |
| Batch list up to 50 | — | — | ✓ 721 only |
| Change price / cancel listing | — | — | ✓ seller |
| Start auction (721 / 1155) | — | — | ✓ owner |
| Bid (+1 native token over lead, cumulative) | connect prompt | ✓ (not seller) | — |
| Withdraw when outbid | — | ✓ | — |
| Cancel auction | — | — | ✓ only with no bids |
| Settle after end | — | ✓ winner | ✓ seller (+ keeper auto, 1s Coston2 / 2s mainnets) |
| Cancel & refund everyone (3d after end) | — | ✓ winner | ✓ seller (+ keeper auto) |
| Make offer (if collection allows) | connect prompt | ✓ | — |
| Raise / withdraw own offer (full refund) | — | ✓ | — |
| Accept / decline an offer | — | — | ✓ owner |
| Reclaim own expired offer (full refund) | — | ✓ bidder (+ keeper auto) | — |
| Enable offers for a collection | — | — | ✓ ERC-173 owner |
| Save search / notifications (SIWE) | disabled + hint | ✓ | ✓ |
| Edit own profile (SIWE) | — | ✓ | ✓ |
| Switch network (keeps path) | ✓ | ✓ | ✓ |
| See who controls the contracts (`/status`, `/api/v1/governance`) | ✓ | ✓ | ✓ |
| Security alerts when control changes (SIWE, opt-out) | — | ✓ | ✓ |
| Theme: System / Light / Dark | ✓ | ✓ | ✓ |
| Anything admin | none exists | none | none |

The buttons carry the same names in the app: **Buy**, **List for sale**, **Change price**,
**Cancel listing**, **Start auction**, **Place bid**, **Withdraw my bid**, **Cancel
auction**, **Settle now**, **Cancel and refund everyone**, **Place offer**, **Withdraw
offer (full refund)**, **Accept**, **Decline**, **Enable offers**.

**Who can settle.** Three parties, and no one else: the marketplace's own keeper (which
settles the moment the auction ends, so normally nobody has to do anything), the seller, and
the winner. A passer-by cannot settle someone else's auction. This never traps money — if
you did not win, your escrow is withdrawable at any time, and if an auction is somehow left
unsettled for three days, the winner, the seller, or the keeper can **Cancel and refund
everyone**, returning every bid.

**Bids must take the lead.** A bid that clears neither the reserve nor the leader's total
plus the flat 1-native-token increment is rejected outright rather than parked as escrow — you never
end up with money locked behind a position that cannot win.

## Badges

Badges are computed from on-chain and indexed facts, never granted by hand.

- **✓ on an NFT** — its collection passed the verifier (a real ERC-721/1155 contract with
  resolvable metadata), the token has a name, its image is served from MagicWebb's own
  store, and its holder is known. Tap the ✓ (or hover) to see which checks a token without
  it has not met.
- **★ Creator** — the holder is the collection's creator and minted the token. A creator
  who merely sells someone else's token shows no star.
- **Collection tiers** on detail headers: *Listed collection* → *Verified* → *Authentic*
  (verified and the creator is known).

## Who controls the contracts

Nobody in the product. On-chain, each network has one **admin** key (upgrades and keeper
rotation, until it is renounced) and one **keeper** (automated settlement — it can never
move or block funds). Both are public: `/status` shows the current admin, pending admin,
keeper and every upgrade or hand-off ever made, and signed-in wallets are notified when
control changes (opt out in your profile).

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
you in the contract. **Profile → the Refunds card → Withdraw.** Never expires. An offer
that expired is returned to you by the keeper within seconds; **Get refund** on the token
page or the Offers page reclaims it yourself (the NFT's owner cannot touch it).

## What nobody can do

- Freeze, move or take your NFT or funds (non-custodial; contracts hold only what you escrow).
- Curate or approve listings — the Verified badge is computed from on-chain facts.
- Change the fee.
- Pause anything administratively. There is no circuit breaker, no operator role, and no
  entry gate — no role can halt listings, bids, offers, buys, cancels, settlements, or
  withdrawals. Normal contract rules (ownership, expiry, authorization, escrow) still
  apply to every action.
- Log in to an admin page. There is none.

## Why durations instead of dates?

The contracts accept exactly fifteen durations (1m, 3m, 5m, 10m, 15m, 30m, 45m, 1h,
2h, 4h, 8h, 12h, 16h, 20h, 24h) and compute the expiry from the block that mines
your transaction. A date picker cannot satisfy that from a wallet (you do not know the
mining block's timestamp), so every time-bound action asks for a duration.
