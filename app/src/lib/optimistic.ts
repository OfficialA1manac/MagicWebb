// Optimistic UI (v3.6 wave 5c): pure reducers each page applies to its own
// state the moment a transaction is confirmed, plus the one refetch rule.
//
// The seam is deliberately minimal — every reducer takes plain rows and
// returns new rows (never mutates), so they unit-test without Svelte. Pages
// call `patch()` then `settleAfterTx(res, load)`: when the backend already
// confirmed it indexed the tx (`TxResult.indexed`, the instant lane) reload
// now, otherwise once after a short grace period. The old "1.5s + 6s" double
// timers are gone.

import type { TxResult } from './tx/runner';

/** Delay before the single fallback refetch when the instant lane did not confirm. */
export const FALLBACK_REFETCH_MS = 2000;

/**
 * One refetch, not two: immediately when the tx is already indexed, else
 * after FALLBACK_REFETCH_MS. Returns the timer id (null when immediate) so a
 * page can clear it on unmount.
 */
export function settleAfterTx(res: Pick<TxResult, 'indexed'> | null | undefined, load: () => unknown, delayMs = FALLBACK_REFETCH_MS): ReturnType<typeof setTimeout> | null {
  if (res?.indexed) { void load(); return null; }
  return setTimeout(() => { void load(); }, delayMs);
}

// ── Listings ─────────────────────────────────────────────────────────────

export interface ListingLike {
  collection: string;
  token_id: string;
  seller: string;
  price_wei: string;
  amount: number;
  standard: string;
  expires_at: string;
  listed_at?: string;
  name?: string;
  image_uri?: string;
  [k: string]: unknown;
}

export interface ListedInput {
  collection: string;
  tokenId: string;
  seller: string;
  priceWei: string;
  durationSec: number;
  standard?: string;
  amount?: number;
  name?: string;
  imageUri?: string;
  nowMs?: number;
}

/** A fresh listing row for the token page / profile "For sale" tab. */
export function applyListed(i: ListedInput): ListingLike {
  const now = i.nowMs ?? Date.now();
  return {
    collection: i.collection.toLowerCase(),
    token_id: String(i.tokenId),
    seller: i.seller.toLowerCase(),
    price_wei: i.priceWei,
    amount: i.amount ?? 1,
    standard: i.standard ?? 'erc721',
    expires_at: new Date(now + i.durationSec * 1000).toISOString(),
    listed_at: new Date(now).toISOString(),
    name: i.name ?? '',
    image_uri: i.imageUri ?? '',
    _optimistic: true,
  };
}

/** Cancelled: the listing is gone. */
export function applyCancelled(_listing: ListingLike | null): null {
  return null;
}

/** New price, everything else unchanged. */
export function applyPriceChanged<T extends { price_wei: string }>(listing: T | null, newPriceWei: string): T | null {
  if (!listing) return null;
  return { ...listing, price_wei: newPriceWei };
}

export interface BoughtResult {
  /** The listing after purchase (always null — it was consumed). */
  listing: null;
  /** The new owner (the buyer), lowercased. */
  owner: string;
}

/** Bought: listing consumed, buyer owns the token. */
export function applyBought(buyer: string): BoughtResult {
  return { listing: null, owner: buyer.toLowerCase() };
}

/** Grid rows: drop the one that was bought/cancelled (by collection + token_id [+ seller]). */
export function removeListingRow<T extends { collection: string; token_id: string; seller?: string }>(rows: T[], key: { collection: string; tokenId: string; seller?: string }): T[] {
  const c = key.collection.toLowerCase();
  const t = String(key.tokenId);
  const s = key.seller?.toLowerCase();
  return rows.filter((r) => !(r.collection.toLowerCase() === c && String(r.token_id) === t && (s === undefined || (r.seller ?? '').toLowerCase() === s)));
}

// ── Auctions ─────────────────────────────────────────────────────────────

export interface AuctionLike {
  highest_bid_wei: string;
  highest_bidder: string;
  ends_at: string;
  [k: string]: unknown;
}

export interface BidInput {
  bidder: string;
  /** The bidder's new cumulative total (what the contract compares), in wei. */
  totalWei: string;
  nowMs?: number;
  /** Anti-snipe window in seconds (contract: 180). */
  antiSnipeSec?: number;
}

/**
 * Bid placed: the bidder leads with their cumulative total; a bid inside the
 * anti-snipe window extends the end by the same window (mirrors the contract).
 */
export function applyBid<T extends AuctionLike>(a: T | null, b: BidInput): T | null {
  if (!a) return null;
  const now = b.nowMs ?? Date.now();
  const win = (b.antiSnipeSec ?? 180) * 1000;
  const endsMs = new Date(a.ends_at).getTime();
  const ends_at = endsMs - now < win ? new Date(now + win).toISOString() : a.ends_at;
  return { ...a, highest_bid_wei: b.totalWei, highest_bidder: b.bidder.toLowerCase(), ends_at };
}

export interface BidRowLike { bidder: string; amount_wei: string; tx_hash: string; placed_at: string }

/** Bids list: prepend the new bid (newest first). */
export function prependBid<T extends BidRowLike>(rows: T[], b: { bidder: string; amountWei: string; txHash: string; nowMs?: number }): T[] {
  const row = { bidder: b.bidder.toLowerCase(), amount_wei: b.amountWei, tx_hash: b.txHash, placed_at: new Date(b.nowMs ?? Date.now()).toISOString() } as T;
  return [row, ...rows.filter((r) => r.tx_hash !== b.txHash)];
}

/** Auction status transitions (settle / cancel / force-cancel). */
export function applyAuctionStatus<T extends { status: string }>(a: T | null, status: 'settled' | 'cancelled'): T | null {
  return a ? { ...a, status } : null;
}

// ── Offers ───────────────────────────────────────────────────────────────

export interface OfferLike {
  offer_id: string;
  bidder: string;
  amount_wei: string;
  status: string;
  expires_at: string;
  [k: string]: unknown;
}

export type OfferAction = 'made' | 'raised' | 'cancelled' | 'accepted' | 'rejected' | 'refunded';

export interface OfferRowInput {
  bidder: string;
  amountWei?: string;
  durationSec?: number;
  nowMs?: number;
  /** Extra fields for a new row (collection, token_id, units, name…). */
  extra?: Record<string, unknown>;
}

/**
 * The bidder's offer row after `action`. One active offer per bidder per
 * token, so rows are matched by bidder (case-insensitive).
 *   made     → new active row (or replaces a stale one)
 *   raised   → amount updated, expiry kept (contract keeps the original expiry)
 *   cancelled/rejected/refunded → row removed
 *   accepted → row removed (the token changed hands)
 */
export function applyOfferRow<T extends OfferLike>(rows: T[], action: OfferAction, i: OfferRowInput): T[] {
  const who = i.bidder.toLowerCase();
  const now = i.nowMs ?? Date.now();
  const rest = rows.filter((r) => r.bidder.toLowerCase() !== who);
  switch (action) {
    case 'made': {
      const row = {
        offer_id: `optimistic:${who}:${now}`,
        bidder: who,
        amount_wei: i.amountWei ?? '0',
        status: 'active',
        expires_at: new Date(now + (i.durationSec ?? 0) * 1000).toISOString(),
        created_at: new Date(now).toISOString(),
        _optimistic: true,
        ...(i.extra ?? {}),
      } as unknown as T;
      return [row, ...rest];
    }
    case 'raised': {
      const cur = rows.find((r) => r.bidder.toLowerCase() === who);
      if (!cur) return rows;
      return rows.map((r) => (r === cur ? { ...r, amount_wei: i.amountWei ?? r.amount_wei } : r));
    }
    case 'cancelled':
    case 'rejected':
    case 'refunded':
    case 'accepted':
      return rest;
  }
}

// ── Refunds (pull-payment balances per core) ─────────────────────────────

export interface RefundRowLike { key: string; wei: bigint; ok: boolean; [k: string]: unknown }

/** Withdrawn from one core: its balance is zero now. */
export function applyRefund<T extends RefundRowLike>(rows: T[], key: string): T[] {
  return rows.map((r) => (r.key === key ? { ...r, wei: 0n } : r));
}
