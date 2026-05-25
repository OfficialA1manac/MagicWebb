# MagicWebb User Guide

## Connect wallet
Open the app → click **Connect** → approve Flare Coston2 network. No registration required.

## Buy a listed token
Browse → click listing → click **Buy** → confirm transaction. Exact price required — NFT transfers to you in the same tx.

## List a single token
1. Go to **List an NFT**
2. Find your token → click **List**
3. Approve the Marketplace contract if prompted (one-time per collection)
4. Enter price (FLR) and duration → click **List**
5. Cancel anytime via **Unlist** — your NFT stays in your wallet

## Batch list (up to 50 tokens at once)
1. Go to **List an NFT** → click **Batch list**
2. Tap tokens to select (up to 50, any collections, green ring = selected)
3. Set a shared price and duration
4. Click **List N tokens** — one wallet confirmation

## Create an auction
1. Open any token you own → click **Auction**
2. Set reserve price (0 = accept any bid), duration, min increment (bps, e.g. 500 = 5%)
3. Click **Create auction** — approve AuctionHouse if prompted
4. End time is fixed. It will never be extended.
5. No bids → click **Cancel**. With bids → keeper bot auto-settles after end time.

## Bid on an auction (2-step commit-reveal)
Bidding uses two steps to prevent front-running:

**Step 1 — Commit**
- Enter bid amount → click **Commit bid** → confirm wallet
- Your bid is hidden on-chain

**Step 2 — Reveal** (after ~2 blocks, ~4 seconds on Flare)
- Return to the auction page — **Reveal bid** button turns green when ready
- Click **Reveal bid** → confirm wallet — bid applied

> Your pending commit is saved in browser storage. Close the tab and return — it reappears.

If outbid: your ETH accumulates in your refund balance. Go to **My Profile → Refunds → Withdraw** to claim it.

## Make an offer
1. Open any token → click **Make offer**
2. Deposit ETH into OfferBook (one-time, reusable for all offers)
3. Set amount and expiry → sign EIP-712 message (no transaction)
4. Owner accepts on-chain → your ETH pays automatically

## Accept an offer
Go to **Offers → Received** → click **Accept** → confirm wallet. NFT goes to bidder, you receive ETH minus 1.5% fee.

## Withdraw refund / deposit
- Outbid refund: **My Profile → Refunds → Withdraw**
- OfferBook deposit: **My Profile → Deposit → Withdraw**

Both are pull-pattern: you initiate, contract sends ETH to you.
