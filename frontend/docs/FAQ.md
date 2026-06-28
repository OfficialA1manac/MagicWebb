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
No. The contracts are immutable and have **no pause switch and no admin override**.
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
| **Successful sale** | **1.5%, paid by the seller** |

You only ever pay network gas for the transaction itself. There are no listing fees,
no bidding fees, and no offer fees.

### Who pays the platform fee?
The **seller**. On any successful sale — a fixed-price buy, a settled auction, or an
accepted offer — a flat **1.5%** is deducted from the seller's proceeds. The seller
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
listed price. The NFT transfers to you and the seller receives the price minus the 1.5%
fee — atomically, in one transaction.

### What happens if two people try to buy the same NFT?
First settle wins. The marketplace is non-exclusive: the same NFT can be listed, in an
auction, and have offers at once. The first transaction to settle takes the NFT; later
attempts revert because the token has moved. No fee is taken on a reverted purchase.

---

## Selling

### How do I list an NFT?
Approve the marketplace contract for your collection once, then list with a price and
expiry (up to 90 days). Listing is free. You keep the NFT in your wallet until someone
buys it.

### How much do I receive on a sale?
98.5% of the sale price. The 1.5% platform fee is deducted at settlement.

### Can I cancel a listing?
Yes, anytime before it sells, for the cost of gas.

---

## Auctions

### How do auctions work?
English (ascending) auctions, up to 7 days. The seller sets a reserve and a minimum bid
increment. Bidders bid for free; each bid escrows the bid amount and refunds the
previous high bidder in full.

### What is anti-snipe protection?
A bid placed within the final 3 minutes extends the auction end time by 3 minutes, so
late snipes can't end an auction before others can respond.

### Who settles an auction?
After the auction ends, anyone (typically the platform's keeper bot) calls `settle`. The
NFT goes to the winner, and the seller receives the winning bid minus the 1.5% fee. If
the seller has moved the NFT or revoked approval, the winner is refunded in full and the
auction cancels — funds are never locked.

---

## Offers

### How do offers work?
Make an offer on any NFT with an amount and an expiry (up to 14 days). The offer amount
is escrowed in the contract — it's free to make and fully refundable. Multiple offers
from the same wallet on the same NFT stack into one position.

### When am I charged?
Never as an offerer. If the owner accepts your offer, you receive the NFT and the seller
receives your offer amount minus the 1.5% fee. If they reject it or it expires, you get
your full principal back.

### Can I withdraw an offer early?
No. A position is locked until it is accepted, rejected by the owner, or expires — then
the principal is refunded. This keeps escrow accounting simple and predictable.

---

## Safety & Trust

### Are the contracts audited?
The contracts pass a full automated test suite and static analysis (Slither). External,
independent audit has been completed.

### What protections are built in?
- Reentrancy guards on every state-changing function (checks-effects-interactions).
- Pull-payment fallback: if a push refund or payout fails, funds are parked for manual
  withdrawal rather than locked.
- Immutable fee recipient and fee rate — they cannot be changed after deployment.
- No admin keys, no pause, no upgrade proxy.

### What happens to my funds if something fails mid-transaction?
Transactions are atomic: either the whole trade completes or it reverts with no fee
taken. Escrowed bids and offer principals are always recoverable via refund or the
pull-withdrawal fallback.
