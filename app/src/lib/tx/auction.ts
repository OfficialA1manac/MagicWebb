// English auctions — AuctionHouse.sol (v3.3).
//   create(coll, id, uint128 reserve, uint64 duration)
//   create1155(coll, id, uint128 amount, uint128 reserve, uint64 duration)
//   Increment rule: ONE for the whole marketplace on every network — taking
//   the lead costs exactly leaderTotal + 1 native token (C2FLR/SGB/FLR).
//   No seller percentage, no per-auction knobs.
//   bid(id) payable — cumulative: msg.value ADDS to your previous bids on this auction
//   settle(id)  cancelEarly(id)  withdrawLoserFunds(id)  refundLosers(id, address[])  withdrawRefund()
import type { Address } from 'viem';
import { auctionHouseAbi } from '../abi';
import { currentChain } from '../chains';
import { fmtPrice } from '../format';
import { ensureOperatorApproval, type TokenStandard } from './approve';
import { TxError } from './errors';
import { assertDuration, assertPrice } from './marketplace';
import { runTx, type TxHooks, type TxRequest, type TxResult } from './runner';

export const MIN_BID_INCREMENT_WEI = 10n ** 18n; // AuctionHouse.MIN_BID_INCREMENT = 1 ether

export function auctionAddress(): Address {
  const a = currentChain().contracts.auctionHouse;
  if (!a) throw new TxError('Invalid', `Trading is not live on ${currentChain().name} yet — browsing, your wallet, and your profile still work. Switch to Coston2 to trade.`);
  return a;
}

// ── builders ───────────────────────────────────────────────────────────────
export function buildCreate(nft: Address, tokenId: bigint, reserveWei: bigint, duration: number, std: TokenStandard = 'erc721', amount = 1n): TxRequest {
  assertPrice(reserveWei); assertDuration(duration);
  if (std === 'erc1155') {
    if (amount < 1n) throw new TxError('Invalid', 'Amount must be at least 1.');
    return { address: auctionAddress(), abi: auctionHouseAbi, functionName: 'create1155', args: [nft, tokenId, amount, reserveWei, BigInt(duration)] };
  }
  return { address: auctionAddress(), abi: auctionHouseAbi, functionName: 'create', args: [nft, tokenId, reserveWei, BigInt(duration)] };
}

export function buildBid(auctionId: bigint, amountWei: bigint): TxRequest {
  if (amountWei <= 0n) throw new TxError('Invalid', 'Enter a bid amount.');
  return { address: auctionAddress(), abi: auctionHouseAbi, functionName: 'bid', args: [auctionId], value: amountWei };
}

export const buildSettle = (id: bigint): TxRequest => ({ address: auctionAddress(), abi: auctionHouseAbi, functionName: 'settle', args: [id] });
export const buildCancelEarly = (id: bigint): TxRequest => ({ address: auctionAddress(), abi: auctionHouseAbi, functionName: 'cancelEarly', args: [id] });
export const buildWithdrawLoserFunds = (id: bigint): TxRequest => ({ address: auctionAddress(), abi: auctionHouseAbi, functionName: 'withdrawLoserFunds', args: [id] });
export const buildWithdrawRefund = (): TxRequest => ({ address: auctionAddress(), abi: auctionHouseAbi, functionName: 'withdrawRefund', args: [] });
export function buildRefundLosers(id: bigint, batch: Address[]): TxRequest {
  if (batch.length === 0 || batch.length > 200) throw new TxError('Invalid', 'Batch must contain 1–200 addresses.');
  return { address: auctionAddress(), abi: auctionHouseAbi, functionName: 'refundLosers', args: [id, batch] };
}

/**
 * Bids are cumulative per bidder. To lead, your total must be ≥ reserve and
 * ≥ current leader + 1 native. Returns the extra amount to send this time.
 */
export function minimumTopUp(opts: { currentHighestWei: bigint; reserveWei: bigint; myCumulativeWei: bigint; minIncrementBps?: number }): bigint {
  // Mirrors AuctionHouse.bid() v3.3: overtaking needs leader + 1 native token,
  // flat, marketplace-wide. Cumulative escrow counts: leader 500, you have 200
  // escrowed → send 301+. (minIncrementBps is accepted and ignored so callers
  // rendering pre-v3.3 auction rows don't break.)
  const target = opts.currentHighestWei > 0n ? opts.currentHighestWei + MIN_BID_INCREMENT_WEI : opts.reserveWei;
  const need = target - opts.myCumulativeWei;
  return need > 0n ? need : 0n;
}

// ── flows ──────────────────────────────────────────────────────────────────
export interface CreateAuctionArgs { nft: Address; tokenId: bigint; reserveWei: bigint; duration: number; std?: TokenStandard; amount?: bigint; name?: string }

export function createAuction(a: CreateAuctionArgs, hooks?: TxHooks): Promise<TxResult> {
  const req = buildCreate(a.nft, a.tokenId, a.reserveWei, a.duration, a.std, a.amount);
  const sym = currentChain().currency;
  return runTx({
    title: `Auction ${a.name ?? `#${a.tokenId}`}`,
    approval: (ctx) => ensureOperatorApproval(ctx, a.nft, auctionAddress(), a.std),
    request: async () => req,
    summary: [
      ['Reserve', `${fmtPrice(a.reserveWei)} ${sym}`],
      ['Anti-snipe', 'Bids in the last 3 minutes extend the auction (up to 30 min total)'],
      ['Marketplace fee', '1.5% · deducted from the winning bid when settled'],
      ['Cost now', 'Free · gas only'],
    ],
  }, hooks);
}

export interface BidArgs { auctionId: bigint; amountWei: bigint; name?: string; myCumulativeWei?: bigint }

export function bid(a: BidArgs, hooks?: TxHooks): Promise<TxResult> {
  const req = buildBid(a.auctionId, a.amountWei);
  const sym = currentChain().currency;
  const total = (a.myCumulativeWei ?? 0n) + a.amountWei;
  return runTx({
    title: `Bid on ${a.name ?? `auction #${a.auctionId}`}`,
    request: async () => req,
    summary: [
      ['You send now', `${fmtPrice(a.amountWei)} ${sym}`],
      ...(a.myCumulativeWei ? [['Your total bid', `${fmtPrice(total)} ${sym}`] as [string, string]] : []),
      ['If outbid', 'Your funds are refundable any time — nothing is lost'],
    ],
  }, hooks);
}

export const settle = (a: { auctionId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Settle ${a.name ?? `auction #${a.auctionId}`}`, request: async () => buildSettle(a.auctionId), summary: [['Who can settle', 'The keeper (automatic), the winner, or the seller'], ['What happens', 'NFT to the winner, proceeds (minus 1.5%) to the seller']] }, hooks);

export const cancelEarly = (a: { auctionId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Cancel ${a.name ?? `auction #${a.auctionId}`}`, request: async () => buildCancelEarly(a.auctionId), summary: [['Allowed when', 'No bids yet · NFT stays with you']] }, hooks);

export const withdrawLoserFunds = (a: { auctionId: bigint; amountWei?: bigint }, hooks?: TxHooks) =>
  runTx({ title: 'Withdraw your bid', request: async () => buildWithdrawLoserFunds(a.auctionId), summary: a.amountWei ? [['Amount', `${fmtPrice(a.amountWei)} ${currentChain().currency}`]] : [] }, hooks);

export const withdrawRefund = (a: { amountWei?: bigint } = {}, hooks?: TxHooks) =>
  runTx({ title: 'Withdraw refund', request: async () => buildWithdrawRefund(), summary: a.amountWei ? [['Amount', `${fmtPrice(a.amountWei)} ${currentChain().currency}`]] : [] }, hooks);
