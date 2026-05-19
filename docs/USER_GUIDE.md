# WebbPlace User Guide

WebbPlace is a non-custodial NFT marketplace on the Flare network. You keep your NFTs and funds in your own wallet at all times — the platform never holds your assets.

---

## Getting Started

### Connecting Your Wallet

WebbPlace works with any EVM-compatible wallet. The easiest options are:

- **MetaMask** (browser extension or mobile) — most common, recommended for desktop
- **WalletConnect** — connects mobile wallets like Rainbow, Trust Wallet, or Coinbase Wallet by scanning a QR code

To connect:

1. Click **Connect Wallet** in the top-right corner of the site.
2. Choose your wallet from the list.
3. Approve the connection request in your wallet app.

You do not need to sign anything to browse. You only need to sign or send transactions when you buy, list, bid, or manage offers.

### Getting Testnet FLR (Coston2)

WebbPlace currently runs on **Coston2**, Flare's test network. Transactions cost gas in C2FLR (testnet FLR), which has no real value and is free to obtain.

1. Go to the [Flare Faucet](https://faucet.flare.network).
2. Select **Coston2** from the network dropdown.
3. Paste your wallet address and click **Request**.
4. C2FLR will appear in your wallet within a few seconds.

You will need a small amount (less than 1 C2FLR) to cover gas for most actions.

### Network Setup

Your wallet needs to be on the Coston2 network to use the marketplace. If you are on the wrong network, a banner will appear prompting you to switch.

**Coston2 network details** (for manual setup in MetaMask):

| Field | Value |
|-------|-------|
| Network name | Coston2 |
| RPC URL | https://coston2-api.flare.network/ext/C/rpc |
| Chain ID | 114 |
| Currency symbol | C2FLR |
| Block explorer | https://coston2-explorer.flare.network |

Most wallets will offer to add Coston2 automatically when you visit the site. Click **Approve** in the wallet prompt.

---

## Browsing & Discovery

### Browse NFTs

The homepage shows recently listed NFTs and trending collections. Scroll to see more. Each tile shows the NFT image, name, collection, and current price (for fixed-price listings) or current bid (for active auctions).

### Search and Filter

Use the search bar at the top to find NFTs by name or token ID. You can filter by:

- **Status:** For sale, In auction, Has offers, All
- **Price range:** Minimum and maximum in FLR
- **Collection:** Limit results to a specific ERC-721 contract

Filters apply immediately — no need to press a search button.

### NFT Detail Page

Click any NFT tile to open the detail page. You will see:

- Full-resolution image and metadata
- Current owner's wallet address
- Listing status: fixed-price, in auction, or unlisted
- Price history and recent sales
- Active offers on this token
- Links to the token on the block explorer

### Collection Pages

Click a collection name to open the collection page, which shows:

- All tracked tokens in the collection
- Floor price (lowest active listing)
- Recent sales volume
- Active auctions

### Trending Collections

The homepage **Trending** section ranks collections by a weighted score of views, bid activity, and recent trading volume, with a time-decay factor so stale activity fades out. Collections with more recent engagement rank higher.

---

## Buying NFTs

### Buy at Fixed Price (Buy Now)

When an NFT has a **Buy Now** price, you can purchase it instantly.

**How it works:**

1. Open the NFT detail page.
2. Click **Buy Now**.
3. Review the price (in FLR) and the platform fee breakdown in the confirmation dialog.
4. Click **Confirm** and approve the transaction in your wallet.
5. Once the transaction confirms on-chain (usually 5–15 seconds on Coston2), the NFT transfers to your wallet.

**Gas fees:** You pay a small gas fee on top of the purchase price. On Coston2, this is typically less than 0.01 C2FLR.

**What happens after purchase:** The smart contract transfers the NFT to your address and splits the payment between the seller and the platform fee recipient in a single atomic transaction. There is no intermediate escrow step — the trade settles in one on-chain call.

**Note:** If the listing expires before you confirm, or if someone else buys it first, the transaction will revert and you will only pay the gas for the failed attempt.

### Bidding in Auctions

Auctions have a defined start time, end time, reserve price, and minimum bid increment.

**Types of auctions:**

- **Reserve auction:** Bidding opens immediately but the auction only commits to selling once the reserve price is met. If the reserve is never reached, no sale occurs and the NFT returns to the seller.
- **Standard timed auction:** Any bid above the minimum commits the auction. The highest bidder at the end time wins.

**How to bid:**

1. Open an auction listing.
2. The current highest bid and time remaining are shown prominently.
3. Enter your bid amount — it must be at least the current bid plus the minimum increment.
4. Click **Place Bid** and confirm the transaction in your wallet.
5. Your bid amount is held in the auction contract until you are outbid or the auction ends.

**If you are outbid:** Your previous bid is automatically refunded to your wallet when the new bid comes in. You do not need to claim it manually.

**Countdown timer:** The timer shows time remaining in the auction. Some auctions have an anti-snipe extension: if a bid is placed in the final few minutes, the end time extends slightly to give other bidders a chance to respond.

**Auto-settlement:** When an auction ends, it can be settled by anyone calling the settlement function (or by the backend keeper bot). The NFT transfers to the winner and the payment goes to the seller automatically.

### Making Off-Chain Offers (EIP-712)

An offer is a signed commitment to buy an NFT at a specific price. Unlike bids in auctions, offers do not lock up any funds on-chain until the seller accepts.

**What an offer is:**

An offer is a message signed with your wallet's private key, saying: "I agree to buy token X from collection Y for Z FLR, valid until [expiry]." The signature is cryptographically verifiable — the seller can confirm it is genuine without you spending any gas.

**How EIP-712 works (plain English):**

EIP-712 is a standard for structured, human-readable message signing. When you make an offer, your wallet shows you the exact terms — token, price, expiry — in a readable format before you sign. You are not sending a transaction; you are producing a signed message. No gas is spent at this step.

The signed offer is stored off-chain (in the WebbPlace backend). The seller sees it in their offer inbox. If the seller decides to accept, *they* send the on-chain transaction that executes the trade, and only then does gas get spent.

**Making an offer:**

1. Open any NFT detail page (the token does not need to be listed for sale).
2. Click **Make Offer**.
3. Enter the price you are willing to pay and an expiry date.
4. Click **Sign Offer** — your wallet will show a structured sign request (not a transaction).
5. Approve the signature. No gas is charged.
6. The offer is submitted to the marketplace and visible to the token's owner.

**Deposit requirement:**

To prevent spam offers, you may need a small amount of wrapped FLR (WFLR) in your wallet. The contract checks that you have sufficient balance to cover your offer when the seller tries to accept. If your balance drops below the offer amount before acceptance, the acceptance transaction will fail.

**Cancelling an offer:**

You can cancel a pending offer from your profile page. Cancellation sends an on-chain transaction that invalidates the signature, spending a small amount of gas.

**Exporting offer JSON:**

On your profile's Sent Offers tab, you can export any offer as a JSON file containing the signed EIP-712 payload. This is useful for debugging or verifying offers independently.

---

## Selling NFTs

### Listing at Fixed Price

To list an NFT at a fixed price:

1. Go to your profile or the NFT detail page and click **List for Sale**.
2. Enter the price in FLR and an expiry date (after which the listing automatically becomes inactive).
3. **Approve the NFT first:** Before the marketplace can transfer the token on sale, you must grant it approval. The UI will prompt you to send an `approve` transaction for the specific token (or `setApprovalForAll` for the collection). This is a one-time step per collection.
4. Once approved, click **Confirm Listing** and sign the listing transaction.
5. The listing is live immediately after the transaction confirms.

**Cancelling a listing:** Open the NFT detail page while connected as the owner and click **Cancel Listing**. This sends an on-chain transaction and costs gas. After cancellation, the NFT stays in your wallet.

**Changing price:** Cancel the current listing and create a new one at the updated price.

### Creating an Auction

1. Go to the NFT detail page or your profile and click **Create Auction**.
2. Set the parameters:
   - **Reserve price:** Minimum amount the auction must reach for a sale to occur. Can be set to zero for no reserve.
   - **Start time:** When bidding opens. Can be set to now or a future time.
   - **End time:** When bidding closes.
   - **Minimum bid increment:** Each new bid must exceed the previous by at least this amount (e.g. 1 FLR).
3. Approve the NFT for the AuctionHouse contract if you have not already (same one-time approval step as fixed-price listings).
4. Confirm the auction creation transaction.

The NFT is transferred to the AuctionHouse contract when the auction starts. If the auction ends without meeting the reserve price, or if you cancel before any bids, the NFT is returned to you.

**You cannot cancel an auction once it has received a bid above the reserve price.**

### Accepting an Offer

When someone makes an offer on one of your NFTs, it appears in your **Offer Inbox**.

1. Go to your profile and open the **Offers Received** tab.
2. Each offer shows the token, offered price, and expiry.
3. Click **Accept** on the offer you want to take.
4. Review the payout (offered price minus platform fee and royalties).
5. Confirm the transaction in your wallet.

The on-chain transaction verifies the offer signature, transfers the NFT to the buyer, and sends the payment to you — all in one step. You do not need to set a separate approval for offers; the approval you granted when listing (if any) covers this, or the contract will prompt for one.

---

## Portfolio & Profile

### Viewing Your NFTs

Your profile page (accessible by clicking your wallet address in the top bar) shows all ERC-721 tokens held by your wallet that are tracked by the marketplace. The list is fetched by scanning the chain for transfer events to your address.

If you own an NFT from a collection not yet tracked by the marketplace, it may not appear. The list of tracked collections is configured by the platform.

### Active Listings

Your profile's **Listed** tab shows all your active fixed-price listings with their prices and expiry dates. Click any listing to manage it.

### Auction History

The **Auctions** tab on your profile shows:
- Auctions you created (as seller), with current bid and time remaining
- Auctions you have bid in (as bidder), showing your bid status

### Offer Inbox

The **Offers** tab has two sections:

- **Received:** Offers made on NFTs you own. Accept or ignore — ignored offers expire automatically.
- **Sent:** Offers you have made on other tokens. You can cancel a sent offer at any time before it is accepted.

---

## Fees & Royalties

### Platform Fee

WebbPlace charges a platform fee on each completed sale. The fee is a percentage of the sale price, deducted automatically by the smart contract at settlement time. The exact fee percentage is displayed in the purchase confirmation dialog before you confirm any transaction.

### Creator Royalties

If the NFT collection has registered royalty information in the RoyaltyRegistry contract, the creator receives a percentage of secondary sales automatically. This happens on-chain at the same time as the platform fee split — no manual step is required.

### How Fees Are Split

When a sale settles, the smart contract distributes the proceeds in a single transaction:

```
Sale price
  → Creator royalty (if registered)
  → Platform fee
  → Seller receives the remainder
```

You can verify the exact split by inspecting the transaction on the block explorer. Everything is on-chain and auditable.

---

## What This Platform Does NOT Support

- **Minting NFTs** — WebbPlace does not create new tokens. Use the collection's native minting interface or a dedicated minting platform.
- **Creating collections** — You cannot deploy a new ERC-721 contract through WebbPlace.
- **Cross-chain bridging** — Assets must already be on Flare (or Coston2). Moving assets from Ethereum or other chains requires a separate bridge.
- **Fiat payments** — All transactions use native FLR. There is no credit card, PayPal, or bank transfer option.
- **Custodial wallets** — WebbPlace never holds your private key. You sign every transaction yourself.

---

## FAQ

**Do I need to create an account?**

No. Your wallet address is your identity. Connect your wallet and you are ready to use the platform.

**What happens if a transaction fails?**

If a transaction reverts (e.g. because a listing expired or someone else bought the NFT first), no funds are transferred. You only lose the gas fee for the failed transaction, which is typically a fraction of a cent in value on Coston2.

**Can I list an NFT that is already in an auction?**

No. An NFT can only have one active sale mechanism at a time. Cancel the auction (if no bids have been placed above reserve) before creating a fixed-price listing.

**How long do offers stay valid?**

Until the expiry date you set when making the offer, or until you cancel the offer on-chain. Expired offers cannot be accepted even if the seller tries.

**I accepted an offer but the transaction failed — what happened?**

The most likely cause is that the buyer's wallet no longer holds enough WFLR to cover the offer amount. The contract checks the buyer's balance at acceptance time. If the buyer spent their funds after making the offer, the acceptance will revert.

**The NFT I own is not showing on my profile. Why?**

The marketplace tracks a curated list of collections. If your NFT is from an untracked collection, it will not appear in the browsing UI. Contact the platform to request a collection be added.

**Is there a fee to list or make offers?**

Listing at a fixed price requires one approval transaction (one-time per collection) plus the listing transaction — both cost only gas. Making an EIP-712 offer is free (just a signature). Fees are only charged when a sale completes.

**How do I verify contract addresses?**

All deployed contract addresses are published in the README and in this guide. You can verify them on the [Coston2 block explorer](https://coston2-explorer.flare.network). The frontend also validates that the addresses in your environment variables match the expected values at startup, throwing an error if there is a mismatch.

**Can I use WebbPlace without MetaMask?**

Yes. Any WalletConnect-compatible wallet works — Rainbow, Trust Wallet, Coinbase Wallet, and others. Tap the WalletConnect option in the connect dialog and scan the QR code with your mobile wallet.

**When will WebbPlace launch on Flare mainnet?**

The project is currently on Coston2 testnet. Mainnet deployment is planned once the platform has been thoroughly tested. No real funds are at risk on testnet.
