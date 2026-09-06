import { describe, it, expect, vi } from 'vitest';
import {
  settleAfterTx, FALLBACK_REFETCH_MS,
  applyListed, applyCancelled, applyPriceChanged, applyBought, removeListingRow,
  applyBid, prependBid, applyAuctionStatus,
  applyOfferRow, applyRefund,
} from './optimistic';

const NOW = Date.parse('2026-09-06T12:00:00Z');
const A = '0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';
const B = '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const COLL = '0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC';

describe('settleAfterTx — one refetch, never two', () => {
  it('reloads immediately when the instant lane indexed the tx', () => {
    const load = vi.fn();
    const t = settleAfterTx({ indexed: true }, load);
    expect(load).toHaveBeenCalledTimes(1);
    expect(t).toBeNull();
  });

  it('reloads once after the grace period otherwise', () => {
    vi.useFakeTimers();
    const load = vi.fn();
    const t = settleAfterTx({ indexed: false }, load);
    expect(t).not.toBeNull();
    expect(load).not.toHaveBeenCalled();
    vi.advanceTimersByTime(FALLBACK_REFETCH_MS - 1);
    expect(load).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(load).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(10_000);
    expect(load).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it('treats a missing result like not-indexed', () => {
    vi.useFakeTimers();
    const load = vi.fn();
    settleAfterTx(undefined, load);
    vi.advanceTimersByTime(FALLBACK_REFETCH_MS);
    expect(load).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
});

describe('listings', () => {
  it('applyListed builds a row with expiry = now + duration, lowercased addresses', () => {
    const l = applyListed({ collection: COLL, tokenId: '7', seller: A, priceWei: '1000', durationSec: 600, nowMs: NOW, name: 'X' });
    expect(l.collection).toBe(COLL.toLowerCase());
    expect(l.seller).toBe(A.toLowerCase());
    expect(l.token_id).toBe('7');
    expect(l.price_wei).toBe('1000');
    expect(new Date(l.expires_at).getTime()).toBe(NOW + 600_000);
    expect(l.amount).toBe(1);
    expect(l.standard).toBe('erc721');
    expect(l._optimistic).toBe(true);
  });

  it('applyCancelled returns null; applyPriceChanged keeps every other field', () => {
    const l = applyListed({ collection: COLL, tokenId: '1', seller: A, priceWei: '5', durationSec: 60, nowMs: NOW });
    expect(applyCancelled(l)).toBeNull();
    const p = applyPriceChanged(l, '9');
    expect(p?.price_wei).toBe('9');
    expect(p?.seller).toBe(l.seller);
    expect(applyPriceChanged(null, '9')).toBeNull();
    expect(l.price_wei).toBe('5'); // no mutation
  });

  it('applyBought consumes the listing and hands ownership to the buyer', () => {
    expect(applyBought(B.toUpperCase())).toEqual({ listing: null, owner: B.toLowerCase() });
  });

  it('removeListingRow drops the matching row only (case-insensitive, optional seller)', () => {
    const rows = [
      { collection: COLL.toLowerCase(), token_id: '1', seller: A.toLowerCase() },
      { collection: COLL.toLowerCase(), token_id: '2', seller: A.toLowerCase() },
      { collection: COLL.toLowerCase(), token_id: '1', seller: B },
    ];
    expect(removeListingRow(rows, { collection: COLL, tokenId: '1', seller: A })).toHaveLength(2);
    expect(removeListingRow(rows, { collection: COLL, tokenId: '1' })).toHaveLength(1);
    expect(removeListingRow(rows, { collection: COLL, tokenId: '9' })).toHaveLength(3);
  });
});

describe('auctions', () => {
  const a = { auction_id: 1, highest_bid_wei: '10', highest_bidder: A.toLowerCase(), ends_at: new Date(NOW + 3600_000).toISOString(), status: 'active' };

  it('applyBid sets the new leader with their cumulative total', () => {
    const r = applyBid(a, { bidder: B, totalWei: '11', nowMs: NOW });
    expect(r?.highest_bid_wei).toBe('11');
    expect(r?.highest_bidder).toBe(B.toLowerCase());
    expect(r?.ends_at).toBe(a.ends_at); // outside the anti-snipe window
    expect(a.highest_bid_wei).toBe('10');
  });

  it('applyBid inside the last 3 minutes extends the end by 3 minutes (contract rule)', () => {
    const soon = { ...a, ends_at: new Date(NOW + 60_000).toISOString() };
    const r = applyBid(soon, { bidder: B, totalWei: '11', nowMs: NOW });
    expect(new Date(r!.ends_at).getTime()).toBe(NOW + 180_000);
  });

  it('applyBid on null is null', () => {
    expect(applyBid(null, { bidder: B, totalWei: '1' })).toBeNull();
  });

  it('prependBid puts the new bid first and dedupes by tx hash', () => {
    const rows = [{ bidder: A, amount_wei: '10', tx_hash: '0x1', placed_at: 'x' }];
    const r = prependBid(rows, { bidder: B, amountWei: '11', txHash: '0x2', nowMs: NOW });
    expect(r.map((x) => x.tx_hash)).toEqual(['0x2', '0x1']);
    expect(prependBid(r, { bidder: B, amountWei: '11', txHash: '0x2', nowMs: NOW })).toHaveLength(2);
  });

  it('applyAuctionStatus flips status', () => {
    expect(applyAuctionStatus(a, 'settled')?.status).toBe('settled');
    expect(applyAuctionStatus(a, 'cancelled')?.status).toBe('cancelled');
    expect(applyAuctionStatus(null, 'settled')).toBeNull();
  });
});

describe('offers', () => {
  const rows = [
    { offer_id: 'o1', bidder: A.toLowerCase(), amount_wei: '5', status: 'active', expires_at: 'e1' },
    { offer_id: 'o2', bidder: B, amount_wei: '6', status: 'active', expires_at: 'e2' },
  ];

  it('made: new active row first, replacing any stale row from the same bidder', () => {
    const r = applyOfferRow(rows, 'made', { bidder: A.toUpperCase(), amountWei: '7', durationSec: 60, nowMs: NOW, extra: { token_id: '3' } });
    expect(r).toHaveLength(2);
    expect(r[0].bidder).toBe(A.toLowerCase());
    expect(r[0].amount_wei).toBe('7');
    expect(r[0].status).toBe('active');
    expect(new Date(r[0].expires_at).getTime()).toBe(NOW + 60_000);
    expect((r[0] as Record<string, unknown>).token_id).toBe('3');
    expect(r[1].offer_id).toBe('o2');
  });

  it('raised: amount changes, expiry stays (the contract keeps the original expiry)', () => {
    const r = applyOfferRow(rows, 'raised', { bidder: A, amountWei: '9' });
    expect(r[0]).toMatchObject({ offer_id: 'o1', amount_wei: '9', expires_at: 'e1' });
    expect(applyOfferRow(rows, 'raised', { bidder: '0x1', amountWei: '9' })).toBe(rows);
  });

  it.each(['cancelled', 'rejected', 'refunded', 'accepted'] as const)('%s removes the bidder row', (action) => {
    const r = applyOfferRow(rows, action, { bidder: B.toUpperCase() });
    expect(r.map((x) => x.offer_id)).toEqual(['o1']);
  });
});

describe('refunds', () => {
  it('applyRefund zeroes the withdrawn core only', () => {
    const rows = [{ key: 'marketplace', wei: 5n, ok: true }, { key: 'auctions', wei: 7n, ok: true }];
    const r = applyRefund(rows, 'auctions');
    expect(r[0].wei).toBe(5n);
    expect(r[1].wei).toBe(0n);
    expect(rows[1].wei).toBe(7n);
  });
});
