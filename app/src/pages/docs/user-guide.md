---
layout: ../../layouts/DocLayout.astro
title: "User Guide"
description: "Listing, bidding, offers, and withdrawals — step by step."
---

# MagicWebb User Guide

New here? Read the [2-minute guide](/docs/start-here/) first.

## Connect wallet
Open the app → click **Connect wallet** → approve Flare Coston2 network. No registration required.

## Buy a listed token
Browse → click listing → click **Buy** → confirm transaction. Exact price required — NFT transfers to you in the same tx.

## List a single token
1. Open a token you own
2. Click **List for sale**
3. Approve the Marketplace contract if prompted (one-time per collection)
4. Enter price (C2FLR) and duration → click **List**
5. Cancel anytime via **Cancel listing** — your NFT stays in your wallet

## Batch list (up to 50 tokens at once)
1. Open your profile's **Your items** tab
2. Tick unlisted tokens to select (up to 50)
3. Set one price and one duration for the whole batch
4. Click **List selected** — one wallet confirmation

## Create an auction
1. Open any token you own → click **Start auction**
2. Set reserve price (at least 1 native token) and end time. The bid increment is the same for every auction on every network: taking the lead costs the current leader's total + 1 native token (C2FLR/SGB/FLR) — cumulative, so what you already have escrowed counts
3. Click **Start auction** — approve AuctionHouse if prompted
4. Auction starts immediately. A bid in the final 3 minutes extends the end time by 3 minutes (anti-snipe).
5. If nobody bids within 30 minutes, the auction is cancelled automatically.
6. To cancel early (only while there are no bids): click **Cancel auction** → approve the wallet transaction.

## Bid on an auction
1. Open an active auction → enter bid amount
2. Click **Place bid** → confirm wallet — bidding is free; you send only your bid amount
3. Bids are cumulative: to take the lead your TOTAL escrow must beat the leader's total by at least 1 native token (e.g. leader at 500 and you have 200 escrowed → send 301+). If you're outbid, your escrow stays in place so you can top up and retake the lead; the moment the auction settles, the keeper refunds every non-winner automatically within seconds. You can also pull your escrow yourself at any time before settlement via **Withdraw my bid**. If an automatic transfer fails (e.g., your wallet is a contract that cannot receive funds), the refund lands on your profile's **Refunds** card and you can withdraw it there any time.
4. At auction end, the platform's keeper bot settles automatically. If the keeper hasn't acted yet, the **auction winner or the seller** can click **Settle now** themselves — no other party can. If an ended auction is never settled, after 3 days the **winner or seller** (or the keeper) can use **Cancel and refund everyone** to release every bidder's escrow — the same parties as settle, and nobody else. On settlement the NFT goes to the winner, and the seller receives the winning bid minus the 2% platform fee (98%).

## Auction fees
- Bidding is free — you send only your bid amount.
- If you win: the seller pays the 2% platform fee, so the seller receives 98% of the winning bid.
- If you lose (outbid) or the seller cancels early: your full bid is refunded — nothing is kept. Most refunds arrive automatically; if a push fails, the amount shows up on your profile's **Refunds** card, where **Withdraw** returns it to your wallet.

## Offer on an NFT
You can offer on any NFT whose collection has offers enabled (the collection's contract owner opts in with **Enable offers** on the collection page), and offering is free:

1. Browse to any token → click **Place offer**
2. Enter offer amount and expiry → confirm wallet (your C2FLR is escrowed on-chain)
3. The owner may accept, decline, or let it expire
4. If accepted: the NFT transfers to you automatically
5. Your offer is free and locked until accepted, declined, withdrawn, or expired — then your full amount is refunded. You can take back your own offer before expiry via **Withdraw offer (full refund)**. Repeat offers on the same NFT stack into one position; expired offers are auto-refunded by the keeper. If the automatic refund push fails, the amount shows up on your profile's **Refunds** card for manual withdrawal.

## Accept an offer (owner)
Go to **Offers → Received** → click **Accept** next to the offer you want → confirm wallet.
NFT goes to bidder, you receive C2FLR minus 2% platform fee (native currency, C2FLR on testnet).

## No royalties
MagicWebb does not pay, route, or enforce royalties of any kind. Sellers receive 98% of the sale price (a flat 2% fee is deducted: 1.5% platform, 0.5% keeper gas fund). The guide uses the native C2FLR currency throughout (Coston2 testnet).
