# What users can and cannot do

MagicWebb has no accounts, no admin and no approval step. Three kinds of visitor exist, and
a person is all three at different moments:

- **Viewer** — no wallet connected.
- **Buyer** — wallet connected on this network, does not own the NFT in question.
- **Seller / owner** — wallet connected, owns the NFT (or created the listing/auction).

Every write is a wallet transaction on the network you are viewing. The marketplace fee is
**1.5%, paid by the seller, only when something sells**. Listing, auctioning, bidding and
offering are free (gas only). Every amount you escrow is refundable.

## Per network

| Capability | Coston2 (114) | Songbird (19) | Flare (14) |
|---|---|---|---|
| Browse, search, collections, activity, docs | ✓ | ✓ | ✓ |
| Connect wallet, view profile + native balance | ✓ | ✓ | ✓ |
| Trade (list, buy, auction, bid, offer, refunds) | ✓ | after audit | after audit |

Songbird and Flare run in **read-only mode** until the contracts pass the external audit
and deploy behind the fee multisig. On those networks every trade surface says
*"Trading isn't live on <network> yet"* and links to the Coston2 site. Switching network
is a navigation to a different origin, so the destination site asks you to connect your
wallet again — nothing about your wallet or funds is shared between networks.

The rest of this document describes a network where trading is live.

## Listings (fixed price)

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| Browse, filter, sort, search, see price history | ✓ | ✓ | ✓ |
| See the Verified NFT badge and what it means | ✓ | ✓ | ✓ |
| Buy (pays the price; receives the NFT in the same tx) | connect first | ✓ | not your own |
| List an NFT (price ≥ 1 native, duration 3m–24h) | — | only NFTs you own | ✓ |
| Change price | — | — | ✓ own listing |
| Cancel listing (NFT never left your wallet) | — | — | ✓ own listing |
| Batch-list up to 50 | — | — | ✓ |
| Save a search / get notifications | — | ✓ (sign-in message) | ✓ |

## Auctions

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| Watch live countdown, bids, anti-snipe extensions | ✓ | ✓ | ✓ |
| Create auction (reserve ≥ 1 native, duration 3m–24h, optional min-increment %) | — | — | ✓ |
| Bid — bids are cumulative; your total must beat the leader by ≥ 1 native | — | ✓ | not your own |
| Be outbid → withdraw your total any time | — | ✓ | — |
| Settle after it ends (keeper does it automatically within seconds) | — | ✓ winner only | ✓ seller |
| Cancel early | — | — | ✓ only while there are no bids |
| Last 3 minutes: every bid extends the end by 3 min (max 30 min total) | rule | rule | rule |

## Offers (escrowed)

| Action | Viewer | Buyer | Seller / owner |
|---|---|---|---|
| See open offers on any NFT | ✓ | ✓ | ✓ |
| Make an offer (amount ≥ 1 native escrowed, duration 3m–24h) | — | ✓ if the collection allows offers | not on your own |
| Raise / lower your offer (keeps original expiry) | — | ✓ | — |
| Cancel your offer → full refund | — | ✓ before expiry | — |
| Accept an offer (NFT → bidder, funds → you minus 1.5%) | — | — | ✓ owner |
| Decline an offer (bidder refunded in full) | — | — | ✓ owner |
| Refund an expired offer (permissionless; keeper also does it) | — | ✓ | ✓ |
| Enable offers for a collection | — | — | ✓ collection **contract** owner (ERC-173) |

## Search & discovery

Everyone: full-text search over NFTs and collections, trending, collection pages with traits,
activity feed, live updates over WebSocket. No results are hidden from viewers.

## Refunds

Outbid? Offer declined? Payment could not be pushed to your wallet? The amount is held for
you in the contract. **Profile → "Refunds waiting for you" → Withdraw.** Never expires.

## What nobody can do

- Freeze, move or take your NFT or funds (non-custodial; contracts hold only what you escrow).
- Curate or approve listings — the Verified NFT badge is computed from on-chain facts.
- Change the fee.
- Pause exits. The multisig that holds `OPERATOR_ROLE` can pause **new** listings, bids and
  offers in an emergency; cancel, settle and withdraw always work.
- Log in to an admin page. There is none.

## Why durations instead of dates?

The contracts accept exactly six durations and compute the expiry from the block that mines
your transaction. A date picker cannot satisfy that from a wallet (you do not know the
mining block's timestamp), so every time-bound action asks for a duration.
