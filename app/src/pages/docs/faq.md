---
layout: ../../layouts/DocLayout.astro
title: "FAQ"
description: "Quick answers: fees, refunds, wallets, and safety."
---

# MagicWebb — Frequently Asked Questions

A non-custodial NFT marketplace on the Flare network. This FAQ answers the questions
new users and traders ask most, in the same spirit as OpenSea, Blur, Magic Eden, and
other marketplaces — adapted to how MagicWebb actually works.

---

## General

### What is MagicWebb?
A non-custodial marketplace for buying, selling, auctioning, and making offers on
ERC-721 and ERC-1155 NFTs. Listings, auctions, bids, and offers are all settled
on-chain by immutable smart contracts. MagicWebb never takes custody of your NFTs or
funds — the contracts move assets directly between buyer and seller.

### What network does it run on?
Flare. The marketplace operates on **Coston2** testnet (chain ID 114).

### What wallet do I need?
Any EVM wallet that supports Flare/Coston2 — MetaMask or WalletConnect-compatible
wallets. You sign in with your wallet (Sign-In-with-Ethereum); there is no email or
password and no account to create.

### Is it custodial? Can MagicWebb freeze or take my NFT?
No. The contracts have **no pause switch and no admin override** over any trade or escrow.
Your NFT stays in your wallet until a sale settles; offers escrow only the bidder's
funds in the contract, refundable until accepted.

---

## Fees

### What does it cost to use MagicWebb?

| Action | Cost |
|--------|------|
| List an NFT (fixed price) | **Free** |
| Create an auction | **Free** |
| Place a bid | **Free** |
| Make an offer | **Free** |
| **Successful sale** | **2%, paid by the seller** (1.5% platform + 0.5% keeper gas fund) |

You only ever pay network gas for the transaction itself. There are no listing fees,
no bidding fees, and no offer fees.

### Who pays the platform fee?
The **seller**. On any successful sale — a fixed-price buy, a settled auction, or an
accepted offer — a flat **2%** is deducted (1.5% to the platform wallet, 0.5% to the network's keeper bot to fund its settlement gas) from the seller's proceeds. The seller
receives **98.5%** of the sale price. Buyers pay exactly the listed price.

### Are bids and offers really free?
Yes. You send only the bid or offer amount; it's held in escrow by the contract. If
you're outbid, your bid is refunded **in full**. If your offer is rejected or expires,
your principal is refunded **in full**. Nothing is kept by the platform unless a sale
completes.

### Are there royalties to creators?
The contracts focus on the platform fee. Creator royalties are not enforced on-chain in
this build.

---

## Buying

### How do I buy a fixed-price NFT?
Open the token page, connect your wallet, and click **Buy Now**. You send exactly the
listed price. The NFT transfers to you and the seller receives the price minus the 2%
fee — atomically, in one transaction.

### What happens if two people try to buy the same NFT?
First settle wins. The marketplace is non-exclusive: the same NFT can be listed, in an
auction, and have offers at once. The first transaction to settle takes the NFT; later
attempts revert because the token has moved. No fee is taken on a reverted purchase.

---

## Selling

### How do I list an NFT?
Approve the marketplace contract for your collection once, then list with a price and
expiry (one of 15 fixed durations: 1min, 3min, 5min, 10min, 15min, 30min, 45min, 1hr, 2hr, 4hr, 8hr, 12hr, 16hr, 20hr, or 24hr). Listing is free. You keep the NFT in your wallet until someone buys it.

### How much do I receive on a sale?
98% of the sale price. The 2% platform fee is deducted at settlement.

### Can I cancel a listing?
Yes, anytime before it sells, for the cost of gas.

---

## Auctions

### How do auctions work?
English (ascending) auctions, up to 24 hours (one of 15 fixed durations, 1min–24hr). The seller sets a reserve. Bidders bid for free; bids must overtake the current leader by at least +1 native token (FLR/SGB/C2FLR). Losers can withdraw early; keeper auto-settles instantly.

### What is anti-snipe protection?
A bid placed within the final 3 minutes extends the auction end time by 3 minutes, so
late snipes can't end an auction before others can respond.

### Who settles an auction?
The platform's keeper bot settles **instantly** the moment the auction expires — you
normally never have to do anything. If the keeper hasn't settled yet, the **auction
winner or the seller** can call `settle` themselves. Apart from addresses holding the
platform's `KEEPER_ROLE`, no one else can settle someone else's auction. The NFT goes to the winner, and the seller receives
the winning bid minus the 2% fee. If the seller has moved the NFT or revoked approval,
the winner is refunded in full and the auction finalises without a sale.

### Can an auction get stuck? Can the seller cancel it?
No, and no. Once any bid clears the reserve the seller can **never** cancel — bidders
committed capital in good faith. And an auction can never be stuck: losing bidders can
withdraw at any time, and if the keeper never settles, after 3 days the **seller or the
winning bidder** can call `forceCancel` to unlock every bidder's escrow (only those
parties and the keeper may trigger it — no outsider can force-cancel someone else's
auction). Nothing on the protocol is pausable, so no one can freeze an auction either.

---

## Offers

### How do offers work?
Make an offer on any NFT in an offer-enabled collection (the collection owner opts in) with an amount and an expiry (one of 15 fixed durations: 1min–24hr). The offer amount is escrowed in the contract — it's free to make and fully refundable. Multiple offers from the same wallet on the same NFT stack into one position. Top-ups do not refresh the expiry timer.

### When am I charged?
Never as an offerer. If the owner accepts your offer, you receive the NFT and the seller
receives your offer amount minus the 2% fee. If they reject it or it expires, you get
your full principal back.

### Can I withdraw an offer early?
Yes. Bidders can cancel their own offer before expiry via `cancelOffer()` for a full principal refund. Once expired, the keeper auto-refunds. The seller can also reject at any time.

---

## Safety & Trust

### Are the contracts audited?
The contracts pass a full automated test suite and static analysis (Slither). External,
independent audit has been completed.

### What protections are built in?
- Reentrancy guards on every state-changing function (checks-effects-interactions).
- Pull-payment fallback: if a push refund or payout fails, funds are parked for manual
  withdrawal rather than locked.
- The fee rate is a compile-time constant — it cannot be changed on any deployment.
- Nothing is pausable — no entry or exit path can ever be halted.
- Every network (Coston2, Songbird, Flare) deploys unsealed with a single per-network
  admin whose only powers are instant contract upgrades, keeper replacement and
  rotating/renouncing its own key — it cannot pause trading or touch funds.
  Immutability is a later, explicit per-network step: on the owner's order the admin
  calls `renounceAdmin()`, after which there are no admin keys, no upgrades, and the
  fee recipient and single keeper are fixed forever.

### What happens to my funds if something fails mid-transaction?
Transactions are atomic: either the whole trade completes or it reverts with no fee
taken. Escrowed bids and offer principals are always recoverable via refund or the
pull-withdrawal fallback.

## Trust & safety

<a id="verified"></a>
### What does the "Verified NFT" badge mean?
Two on-chain facts, checked automatically by the indexer and re-checked daily — no human
curation, no application form:

1. The collection contract answers ERC-165 `supportsInterface()` as a standard **ERC-721**
   or **ERC-1155** NFT.
2. Its token metadata (name/image) has resolved at least once.

It does **not** mean MagicWebb vouches for the art, the creator, or the seller. "Unverified"
means one of the two checks has not passed yet — often just a brand-new collection whose
metadata has not loaded. Anyone can list from any collection either way.

<a id="creator"></a>
### What does the "★ Creator" badge mean?
The address shown is the **on-chain owner of the collection contract** (ERC-173
`owner()`), detected automatically by the verifier sweep. It appears on the collection
page, on token pages, and on that address's profile. When a collection is Verified AND
its creator is known, token pages upgrade the checkmark to **"✓ Authentic — {collection}
by {creator}"** so you can tie an NFT to its collection and creator. Like the Verified
badge, it is computed from on-chain facts — not a curation or endorsement.

<a id="holder-badge"></a>
### What is the collector badge next to an owner's name?
A fun, deterministic label (e.g. *SharinganHodl*, *PirateKingHodl*) derived purely from
the wallet address — the same wallet always gets the same name. It carries no on-chain
meaning, no privileges, and no judgement; it just makes owners easier to recognise.

### Why did my listing, auction or offer need a duration instead of a date?
The contracts accept fifteen fixed durations (1 min – 24 h) and compute
the expiry on-chain from the block that mines your transaction. Picking a duration is the
only way a wallet can satisfy that rule; the exact expiry is shown once the transaction
confirms.

### Where do refunds go?
If you are outbid, an offer you made is declined, or a payment could not be delivered, the
amount is held for you in the contract. Your profile shows "Refunds waiting for you" with a
one-tap Withdraw. Nothing expires and nothing is taken.
