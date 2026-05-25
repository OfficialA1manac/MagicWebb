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
2. Tap tokens to select (up to 50, any collections)
3. Set price and duration per token
4. Click **List N tokens** — one wallet confirmation

## Create an auction
1. Open any token you own → click **Auction**
2. Set reserve price (0 = accept any bid), end time, min increment (bps, e.g. 500 = 5%)
3. Click **Create auction** — approve AuctionHouse if prompted
4. Auction starts immediately. End time is fixed and never extended.
5. If nobody bids within 30 minutes, the auction is cancelled automatically.
6. To cancel early: click **Cancel Auction** → approve the wallet transaction manually.

## Bid on an auction
1. Open an active auction → enter bid amount
2. Click **Bid** → confirm wallet — your bid + 1.5% fee is sent in one transaction
3. If someone outbids you, your full payment (bid + fee) is returned to your wallet automatically — no action needed
4. At auction end, the keeper bot settles automatically: NFT goes to winner, seller receives the full bid amount

## Auction fees for bidders
- You pay: `bid + 1.5% of bid` upfront
- If you win: the 1.5% goes to the platform, seller gets the full bid
- If you lose: you get back the full amount you paid (bid + fee) — no fee kept

## Offer on an NFT
Offers work on tokens the owner has marked as eligible:

1. Browse token → if owner enabled offers, click **Make Offer**
2. Enter offer amount → click **Submit Offer** → confirm wallet (ETH deposited on-chain)
3. Owner reviews and may accept or ignore
4. If accepted: NFT transfers to you automatically
5. To cancel: click **Withdraw Offer** → confirm wallet → full ETH returned, no fee

## Enable offers on your NFT (owner)
1. Open any token you own → click **Accept Offers**
2. Confirm wallet — token is now marked eligible
3. Bidders can make on-chain ETH offers
4. Review offers in **My Offers → Received** → click **Accept** on your preferred offer
5. To stop receiving offers: click **Remove Eligibility** → confirm wallet

## Accept an offer (owner)
Go to **Offers → Received** → click **Accept** next to the offer you want → confirm wallet.
NFT goes to bidder, you receive ETH minus 1.5% platform fee.

## No royalties
MagicWebb does not pay, route, or enforce royalties of any kind. Sellers receive 100% of the sale price minus the 1.5% platform fee.
