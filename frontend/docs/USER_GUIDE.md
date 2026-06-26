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
4. Auction starts immediately. A bid in the final 3 minutes extends the end time by 3 minutes (anti-snipe).
5. If nobody bids within 30 minutes, the auction is cancelled automatically.
6. To cancel early: click **Cancel Auction** → approve the wallet transaction manually.

## Bid on an auction
1. Open an active auction → enter bid amount
2. Click **Bid** → confirm wallet — bidding is free; you send only your bid amount
3. If someone outbids you, your full bid is returned to your wallet automatically — no action needed
4. At auction end, the keeper bot settles automatically: NFT goes to winner, seller receives the winning bid minus the 1.5% platform fee (98.5%)

## Auction fees
- Bidding is free — you send only your bid amount.
- If you win: the seller pays the 1.5% platform fee, so the seller receives 98.5% of the winning bid.
- If you lose (outbid) or the seller cancels early: your full bid is refunded — nothing is kept.

## Offer on an NFT
You can offer on **any** NFT — there is no eligibility gate, and offering is free:

1. Browse to any token → click **Make Offer**
2. Enter offer amount and expiry → click **Submit Offer** → confirm wallet (your FLR is escrowed on-chain)
3. The owner may accept, reject, or let it expire
4. If accepted: the NFT transfers to you automatically
5. Your offer is free and locked until accepted, rejected, or expired — then your full amount is refunded. Repeat offers on the same NFT stack into one position; there is no early withdrawal.

## Accept an offer (owner)
Go to **Offers → Received** → click **Accept** next to the offer you want → confirm wallet.
NFT goes to bidder, you receive ETH minus 1.5% platform fee.

## No royalties
MagicWebb does not pay, route, or enforce royalties of any kind. Sellers receive 98.5% of the sale price (a flat 1.5% platform fee is deducted).
