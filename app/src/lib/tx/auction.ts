// English auctions — AuctionHouse.sol (v3.3).
//   create(coll, id, uint128 reserve, uint64 duration)
//   create1155(coll, id, uint128 amount, uint128 reserve, uint64 duration)
//   Increment rule: ONE for the whole marketplace on every network — taking
//   the lead costs exactly leaderTotal + 1 native token (C2FLR/SGB/FLR).
//   No seller percentage, no per-auction knobs.
//   bid(id) payable — cumulative: msg.value ADDS to your previous bids on this auction
//   settle(id)  cancelEarly(id)  forceCancel(id)  withdrawLoserFunds(id)  refundLosers(id, address[])  withdrawRefund()
//   forceCancel: seller/winner/keeper only, unlocks at endsAt + 3 days when
//   settle() is permanently impossible — marks settled so refundLosers() frees
//   every bidder's escrow. NFT stays where it is.
import type { Address } from 'viem';
import { auctionHouseAbi } from '../abi';
import { currentChain } from '../chains';
import { fmtPrice } from '../format';
import { ensureOperatorApproval, type TokenStandard } from './approve';
import { isValidDuration, type DurationSeconds } from './durations';
import { TxError } from './errors';
import { assertPrice, durationLabel, feeWei } from './marketplace';
import { runTx, type TxHooks, type TxRequest, type TxResult } from './runner';

export const MIN_BID_INCREMENT_WEI = 10n ** 18n; // AuctionHouse.MIN_BID_INCREMENT = 1 ether
export const FORCE_CANCEL_WINDOW_SEC = 3 * 86400; // AuctionHouse.SELLER_DEFAULT_WINDOW = 3 days

/** Same rule as MarketplaceCore: only the shared 14 durations are accepted on-chain. */
export function assertAuctionDuration(d: number): asserts d is DurationSeconds {
  if (!isValidDuration(d)) throw new TxError('Invalid', 'Pick one of the allowed durations (1m–24h).');
}

/** True once forceCancel() is callable for an auction that ended at `endsAtSec` (unix seconds). */
export function forceCancelUnlocked(endsAtSec: number, nowMs = Date.now()): boolean {
  return nowMs / 1000 >= endsAtSec + FORCE_CANCEL_WINDOW_SEC;
}

export function auctionAddress(): Address {
  const a = currentChain().contracts.auctionHouse;
  if (!a) throw new TxError('Invalid', `Trading is not live on ${currentChain().name} yet — browsing, your wallet, and your profile still work. Switch to a live trading network to trade.`);
  return a;
}

// ── builders ───────────────────────────────────────────────────────────────
export function buildCreate(nft: Address, tokenId: bigint, reserveWei: bigint, duration: number, std: TokenStandard = 'erc721', amount = 1n): TxRequest {
  assertPrice(reserveWei); assertAuctionDuration(duration);
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
export const buildForceCancel = (id: bigint): TxRequest => ({ address: auctionAddress(), abi: auctionHouseAbi, functionName: 'forceCancel', args: [id] });
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
      ['You receive', `${fmtPrice(a.reserveWei - feeWei(a.reserveWei))} ${sym} or more when it settles (2% fee)`],
      ['Listing', 'Free'],
      ['Ends in', durationLabel(a.duration)],
    ],
    success: { message: 'Auction started!', action: { label: 'See live auctions', href: '/auctions' } },
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
      ['Your bid', `${fmtPrice(a.amountWei)} ${sym}`],
      ...(a.myCumulativeWei ? [['Your total bid', `${fmtPrice(total)} ${sym}`] as [string, string]] : []),
      ['Held safely', 'Until the auction ends'],
      ['If outbid', 'Refundable'],
    ],
    success: { message: 'Bid placed', action: { label: 'Watch the auction', href: `/auction/${a.auctionId}` } },
  }, hooks);
}

export const settle = (a: { auctionId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Settle ${a.name ?? `auction #${a.auctionId}`}`, request: async () => buildSettle(a.auctionId), summary: [['Who can settle', 'The keeper (automatic), the winner, or the seller'], ['What happens', 'NFT to the winner, proceeds (minus 2%) to the seller']], success: { message: 'Auction settled', action: { label: 'View in your profile', href: '/profile' } } }, hooks);

export const cancelEarly = (a: { auctionId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Cancel ${a.name ?? `auction #${a.auctionId}`}`, request: async () => buildCancelEarly(a.auctionId), summary: [['Allowed when', 'No bids yet · NFT stays with you']], success: { message: 'Auction cancelled', action: { label: 'View in your profile', href: '/profile' } } }, hooks);

export const forceCancel = (a: { auctionId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Force-cancel ${a.name ?? `auction #${a.auctionId}`}`, request: async () => buildForceCancel(a.auctionId), summary: [['Allowed when', 'Ended 3+ days ago and still unsettled · seller, winner, or keeper'], ['What happens', 'Auction closes without a trade — every bid becomes refundable, the NFT stays where it is']] }, hooks);

export const withdrawLoserFunds = (a: { auctionId: bigint; amountWei?: bigint }, hooks?: TxHooks) =>
  runTx({ title: 'Withdraw your bid', request: async () => buildWithdrawLoserFunds(a.auctionId), summary: a.amountWei ? [['Amount', `${fmtPrice(a.amountWei)} ${currentChain().currency}`]] : [], success: { message: 'Bid withdrawn to your wallet' } }, hooks);

export const withdrawRefund = (a: { amountWei?: bigint } = {}, hooks?: TxHooks) =>
  runTx({ title: 'Withdraw refund', request: async () => buildWithdrawRefund(), summary: a.amountWei ? [['Amount', `${fmtPrice(a.amountWei)} ${currentChain().currency}`]] : [], success: { message: 'Refund sent to your wallet' } }, hooks);
