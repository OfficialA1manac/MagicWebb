// `window.MW` — the single bridge between inline page scripts (Astro pages are
// static HTML with vanilla <script>s) and the typed tx/ws/auth libraries.
// Mounted once by <MwRuntime client:load /> in BaseLayout.
import type { Address } from 'viem';
import { runWithModal } from './stores/txmodal.svelte';
import * as M from './tx/marketplace';
import * as A from './tx/auction';
import * as O from './tx/offers';
import * as C from './tx/core';
import type { TxResult } from './tx/runner';
import { connectedAddress, connectedChainId, onAccountChange, requireWallet } from './tx/client';
import { authFetch, authenticate, getSession, logout } from './auth/siwe';
import { ws } from './ws/client';
import { ACTIVITY_CHANNEL, collectionChannel, tokenChannel, userChannel, txChannel } from './ws/channels';
import { currentChain, explorerTx, networkOrigins, chainName } from './chains';
import { esc, fmtPrice, shortAddr, timeAgo, fmtCountdown, toWei } from './format';
import { DURATIONS, DEFAULT_DURATION } from './tx/durations';

const addr = (s: string) => s as Address;
const big = (v: string | number | bigint) => BigInt(v);

function flow<T extends TxResult>(title: string, summary: Array<[string, string]> | undefined, hasApproval: boolean, run: (hooks: Parameters<typeof M.buy>[1]) => Promise<T>, successAction?: { label: string; href: string }) {
  return runWithModal({ title, summary, hasApproval, successAction }, run);
}

export const MW = {
  // ── identity ───────────────────────────────────────────────────────────
  chain: currentChain,
  chainName,
  networkOrigins,
  explorerTx,
  address: connectedAddress,
  chainId: connectedChainId,
  onAccountChange,
  connect: () => requireWallet().then((c) => c.account),
  signIn: authenticate,
  signOut: logout,
  session: getSession,
  authFetch,

  // ── formatting ─────────────────────────────────────────────────────────
  esc, fmtPrice, shortAddr, timeAgo, fmtCountdown, toWei,
  DURATIONS, DEFAULT_DURATION,

  // ── real-time ──────────────────────────────────────────────────────────
  ws,
  ch: { token: tokenChannel, collection: collectionChannel, user: userChannel, tx: txChannel, activity: ACTIVITY_CHANNEL },

  // ── listings ───────────────────────────────────────────────────────────
  buy: (p: { nft: string; tokenId: string; seller: string; priceWei: string; name?: string }) =>
    flow(`Buy ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => M.buy({ nft: addr(p.nft), tokenId: big(p.tokenId), seller: addr(p.seller), priceWei: big(p.priceWei), name: p.name }, h), { label: 'View in my profile', href: '/profile' }),
  list: (p: { nft: string; tokenId: string; priceWei: string; duration: number; std?: 'erc721' | 'erc1155'; amount?: string; name?: string }) =>
    flow(`List ${p.name ?? `#${p.tokenId}`}`, undefined, true, (h) => M.list({ nft: addr(p.nft), tokenId: big(p.tokenId), priceWei: big(p.priceWei), duration: p.duration, std: p.std, amount: p.amount ? big(p.amount) : undefined, name: p.name }, h)),
  cancelListing: (p: { nft: string; tokenId: string; name?: string }) =>
    flow(`Cancel listing ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => M.cancel({ nft: addr(p.nft), tokenId: big(p.tokenId), name: p.name }, h)),
  editPrice: (p: { nft: string; tokenId: string; newPriceWei: string; name?: string }) =>
    flow(`Change price ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => M.editPrice({ nft: addr(p.nft), tokenId: big(p.tokenId), newPriceWei: big(p.newPriceWei), name: p.name }, h)),
  batchList: (p: { items: Array<{ nft: string; tokenId: string; priceWei: string; duration: number }>; name?: string }) =>
    flow(p.name ?? `List ${p.items.length} NFTs`, undefined, true, (h) => M.batchList({ items: p.items.map((i) => ({ coll: addr(i.nft), id: big(i.tokenId), price: big(i.priceWei), duration: i.duration })), name: p.name }, h)),

  // ── auctions ───────────────────────────────────────────────────────────
  createAuction: (p: { nft: string; tokenId: string; reserveWei: string; duration: number; minIncBps?: number; std?: 'erc721' | 'erc1155'; amount?: string; name?: string }) =>
    flow(`Auction ${p.name ?? `#${p.tokenId}`}`, undefined, true, (h) => A.createAuction({ nft: addr(p.nft), tokenId: big(p.tokenId), reserveWei: big(p.reserveWei), duration: p.duration, minIncBps: p.minIncBps, std: p.std, amount: p.amount ? big(p.amount) : undefined, name: p.name }, h), { label: 'See live auctions', href: '/auctions' }),
  bid: (p: { auctionId: string; amountWei: string; name?: string; myCumulativeWei?: string }) =>
    flow(`Bid on ${p.name ?? `auction #${p.auctionId}`}`, undefined, false, (h) => A.bid({ auctionId: big(p.auctionId), amountWei: big(p.amountWei), name: p.name, myCumulativeWei: p.myCumulativeWei ? big(p.myCumulativeWei) : undefined }, h)),
  settle: (p: { auctionId: string; name?: string }) => flow(`Settle ${p.name ?? `auction #${p.auctionId}`}`, undefined, false, (h) => A.settle({ auctionId: big(p.auctionId), name: p.name }, h)),
  cancelAuction: (p: { auctionId: string; name?: string }) => flow(`Cancel ${p.name ?? `auction #${p.auctionId}`}`, undefined, false, (h) => A.cancelEarly({ auctionId: big(p.auctionId), name: p.name }, h)),
  withdrawLoserFunds: (p: { auctionId: string; amountWei?: string }) => flow('Withdraw your bid', undefined, false, (h) => A.withdrawLoserFunds({ auctionId: big(p.auctionId), amountWei: p.amountWei ? big(p.amountWei) : undefined }, h)),
  withdrawRefund: (p: { amountWei?: string } = {}) => flow('Withdraw refund', undefined, false, (h) => A.withdrawRefund({ amountWei: p.amountWei ? big(p.amountWei) : undefined }, h)),
  minimumTopUp: (p: { currentHighestWei: string; reserveWei: string; myCumulativeWei?: string }) =>
    A.minimumTopUp({ currentHighestWei: big(p.currentHighestWei), reserveWei: big(p.reserveWei), myCumulativeWei: big(p.myCumulativeWei ?? 0) }).toString(),

  // ── refunds (all cores) ────────────────────────────────────────────────
  pendingReturns: (who: string) => C.pendingReturns(addr(who)),
  withdrawRefundFrom: (p: { core: string; label?: string; amountWei?: string }) =>
    flow(`Withdraw refund${p.label ? ' · ' + p.label : ''}`, undefined, false, (h) => C.withdrawRefundFrom({ core: addr(p.core), label: p.label, amountWei: p.amountWei ? big(p.amountWei) : undefined }, h)),

  // ── offers ─────────────────────────────────────────────────────────────
  isOfferEligible: (nft: string) => O.isOfferEligible(addr(nft)),
  makeOffer: (p: { nft: string; tokenId: string; principalWei: string; duration: number; std?: 'erc721' | 'erc1155'; units?: string; name?: string }) =>
    flow(`Offer on ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => O.makeOffer({ nft: addr(p.nft), tokenId: big(p.tokenId), principalWei: big(p.principalWei), duration: p.duration, std: p.std, units: p.units ? big(p.units) : undefined, name: p.name }, h), { label: 'See my offers', href: '/offers' }),
  acceptOffer: (p: { nft: string; tokenId: string; bidder: string; principalWei: string; std?: 'erc721' | 'erc1155'; name?: string }) =>
    flow(`Accept offer on ${p.name ?? `#${p.tokenId}`}`, undefined, true, (h) => O.acceptOffer({ nft: addr(p.nft), tokenId: big(p.tokenId), bidder: addr(p.bidder), principalWei: big(p.principalWei), std: p.std, name: p.name }, h)),
  cancelOffer: (p: { nft: string; tokenId: string; name?: string }) => flow(`Cancel offer on ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => O.cancelOffer({ nft: addr(p.nft), tokenId: big(p.tokenId), name: p.name }, h)),
  rejectOffer: (p: { nft: string; tokenId: string; bidder: string; name?: string }) => flow(`Decline offer on ${p.name ?? `#${p.tokenId}`}`, undefined, false, (h) => O.rejectOffer({ nft: addr(p.nft), tokenId: big(p.tokenId), bidder: addr(p.bidder), name: p.name }, h)),
  refundExpiredOffer: (p: { nft: string; tokenId: string; bidder: string }) => flow('Refund expired offer', undefined, false, (h) => O.refundExpiredOffer({ nft: addr(p.nft), tokenId: big(p.tokenId), bidder: addr(p.bidder) }, h)),
  setOfferEligible: (p: { nft: string; eligible: boolean; name?: string }) => flow(`${p.eligible ? 'Enable' : 'Disable'} offers`, undefined, false, (h) => O.setOfferEligible({ nft: addr(p.nft), eligible: p.eligible, name: p.name }, h)),
};

export type MwApi = typeof MW;

export function installMW(): MwApi {
  if (typeof window !== 'undefined') {
    window.MW = MW;
    window.dispatchEvent(new CustomEvent('mw-ready'));
  }
  return MW;
}

/** For page scripts: resolves when window.MW exists. */
export function whenMW(): Promise<MwApi> {
  if (typeof window === 'undefined') return Promise.reject(new Error('no window'));
  if (window.MW) return Promise.resolve(window.MW);
  return new Promise((res) => window.addEventListener('mw-ready', () => res(window.MW as MwApi), { once: true }));
}
