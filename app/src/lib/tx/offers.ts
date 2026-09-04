// Escrowed offers — OfferBook.sol.
//   makeOffer(coll, id, uint128 principal, uint64 duration) payable (msg.value == principal)
//   makeOffer1155(coll, id, uint128 principal, uint128 units, uint64 duration) payable
//   acceptOffer(coll, id, bidder, uint128 expectedPrincipal)  cancelOffer(coll,id)
//   rejectOffer(coll,id,bidder)  refundExpiredOffer(coll,id,bidder)  setOfferEligible(coll,bool)
// A collection must be offer-eligible (owner opt-in) before anyone can offer on it.
import type { Address } from 'viem';
import { offerBookAbi } from '../abi';
import { currentChain } from '../chains';
import { fmtPrice } from '../format';
import { ensureOperatorApproval, type TokenStandard } from './approve';
import { publicClient } from './client';
import { TxError } from './errors';
import { assertDuration, assertPrice, durationLabel, feeWei, tokenHref } from './marketplace';
import { runTx, type TxHooks, type TxRequest, type TxResult } from './runner';

export function offerBookAddress(): Address {
  const a = currentChain().contracts.offerBook;
  if (!a) throw new TxError('Invalid', `Trading is not live on ${currentChain().name} yet — browsing, your wallet, and your profile still work. Switch to a live trading network to trade.`);
  return a;
}

// ── builders ───────────────────────────────────────────────────────────────
export function buildMakeOffer(nft: Address, tokenId: bigint, principalWei: bigint, duration: number, std: TokenStandard = 'erc721', units = 1n): TxRequest {
  assertPrice(principalWei); assertDuration(duration);
  if (std === 'erc1155') {
    if (units < 1n) throw new TxError('Invalid', 'Units must be at least 1.');
    return { address: offerBookAddress(), abi: offerBookAbi, functionName: 'makeOffer1155', args: [nft, tokenId, principalWei, units, BigInt(duration)], value: principalWei };
  }
  return { address: offerBookAddress(), abi: offerBookAbi, functionName: 'makeOffer', args: [nft, tokenId, principalWei, BigInt(duration)], value: principalWei };
}
export const buildAcceptOffer = (nft: Address, tokenId: bigint, bidder: Address, expectedPrincipalWei: bigint): TxRequest =>
  ({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'acceptOffer', args: [nft, tokenId, bidder, expectedPrincipalWei] });
export const buildCancelOffer = (nft: Address, tokenId: bigint): TxRequest =>
  ({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'cancelOffer', args: [nft, tokenId] });
export const buildRejectOffer = (nft: Address, tokenId: bigint, bidder: Address): TxRequest =>
  ({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'rejectOffer', args: [nft, tokenId, bidder] });
export const buildRefundExpiredOffer = (nft: Address, tokenId: bigint, bidder: Address): TxRequest =>
  ({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'refundExpiredOffer', args: [nft, tokenId, bidder] });
export const buildSetOfferEligible = (nft: Address, eligible: boolean): TxRequest =>
  ({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'setOfferEligible', args: [nft, eligible] });

/** On-chain read: can this collection receive offers? */
export async function isOfferEligible(nft: Address): Promise<boolean> {
  const pub = await publicClient();
  return pub.readContract({ address: offerBookAddress(), abi: offerBookAbi, functionName: 'offerEligible', args: [nft] }) as Promise<boolean>;
}

// ── flows ──────────────────────────────────────────────────────────────────
export interface MakeOfferArgs { nft: Address; tokenId: bigint; principalWei: bigint; duration: number; std?: TokenStandard; units?: bigint; name?: string }

export function makeOffer(a: MakeOfferArgs, hooks?: TxHooks): Promise<TxResult> {
  const req = buildMakeOffer(a.nft, a.tokenId, a.principalWei, a.duration, a.std, a.units);
  const sym = currentChain().currency;
  return runTx({
    title: `Offer on ${a.name ?? `#${a.tokenId}`}`,
    request: async () => {
      if (!(await isOfferEligible(a.nft))) throw new TxError('OffersNotEligible', 'This collection has not enabled offers yet. Its owner can turn them on from the collection page.');
      return req;
    },
    summary: [
      ['Held safely', `${fmtPrice(a.principalWei)} ${sym}`],
      ['If not accepted', 'Refunded'],
      ['Expires in', durationLabel(a.duration)],
    ],
    success: { message: 'Offer sent', action: { label: 'See your offers', href: '/offers' } },
  }, hooks);
}

export const acceptOffer = (a: { nft: Address; tokenId: bigint; bidder: Address; principalWei: bigint; std?: TokenStandard; name?: string }, hooks?: TxHooks) => {
  const sym = currentChain().currency;
  const fee = feeWei(a.principalWei);
  return runTx({
    title: `Accept offer on ${a.name ?? `#${a.tokenId}`}`,
    approval: (ctx) => ensureOperatorApproval(ctx, a.nft, offerBookAddress(), a.std),
    request: async () => buildAcceptOffer(a.nft, a.tokenId, a.bidder, a.principalWei),
    summary: [
      ['Offer', `${fmtPrice(a.principalWei)} ${sym}`],
      ['You receive', `${fmtPrice(a.principalWei - fee)} ${sym} (2% fee)`],
      ['The buyer gets', 'The NFT instantly'],
    ],
    success: { message: `Sold ${a.name ?? `#${a.tokenId}`}`, action: { label: 'View in your profile', href: '/profile' } },
  }, hooks);
};

export const cancelOffer = (a: { nft: Address; tokenId: bigint; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Cancel offer on ${a.name ?? `#${a.tokenId}`}`, request: async () => buildCancelOffer(a.nft, a.tokenId), summary: [['Refund', 'Full amount back to your wallet']], success: { message: 'Offer cancelled — funds returned', action: { label: 'See your offers', href: '/offers' } } }, hooks);

export const rejectOffer = (a: { nft: Address; tokenId: bigint; bidder: Address; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `Decline offer on ${a.name ?? `#${a.tokenId}`}`, request: async () => buildRejectOffer(a.nft, a.tokenId, a.bidder), summary: [['Effect', 'Bidder is refunded in full']], success: { message: 'Offer declined', action: { label: 'View listing', href: tokenHref(a.nft, a.tokenId) } } }, hooks);

export const refundExpiredOffer = (a: { nft: Address; tokenId: bigint; bidder: Address }, hooks?: TxHooks) =>
  runTx({ title: 'Refund expired offer', request: async () => buildRefundExpiredOffer(a.nft, a.tokenId, a.bidder), success: { message: 'Expired offer refunded' } }, hooks);

export const setOfferEligible = (a: { nft: Address; eligible: boolean; name?: string }, hooks?: TxHooks) =>
  runTx({ title: `${a.eligible ? 'Enable' : 'Disable'} offers for ${a.name ?? 'collection'}`, request: async () => buildSetOfferEligible(a.nft, a.eligible), summary: [['Who can do this', 'Only the collection owner']], success: { message: a.eligible ? 'Offers enabled' : 'Offers disabled' } }, hooks);
