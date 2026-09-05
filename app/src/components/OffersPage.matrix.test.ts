// Offers viewer/connected/expired matrix (spec B4 "Offers") — pure exports
// from the component's module script, same pattern as TokenPage.actionZone.
import { describe, it, expect } from 'vitest';
import { offerActions, defaultOffersSort, sortOfferRows, isOfferExpired } from './OffersPage.svelte';

describe('OffersPage actions matrix', () => {
  it('Received, not expired → Accept + Decline', () => {
    expect(offerActions('received', false)).toEqual([
      { kind: 'accept', label: 'Accept' },
      { kind: 'decline', label: 'Decline' },
    ]);
  });

  it('Received, expired → Return their funds', () => {
    expect(offerActions('received', true)).toEqual([{ kind: 'return-funds', label: 'Return their funds' }]);
  });

  it('Sent, not expired → Raise offer + Withdraw offer (full refund)', () => {
    expect(offerActions('sent', false)).toEqual([
      { kind: 'raise', label: 'Raise offer' },
      { kind: 'withdraw', label: 'Withdraw offer (full refund)' },
    ]);
  });

  it('Sent, expired → Get refund', () => {
    expect(offerActions('sent', true)).toEqual([{ kind: 'get-refund', label: 'Get refund' }]);
  });
});

describe('OffersPage sort', () => {
  it('Best offer is the default on Received; Newest on Sent', () => {
    expect(defaultOffersSort('received')).toBe('best');
    expect(defaultOffersSort('sent')).toBe('newest');
  });

  const row = (amount: string, created: string, expires: string) => ({ amount_wei: amount, created_at: created, expires_at: expires });
  const rows = [
    row('1000000000000000000', '2026-09-01T00:00:00Z', '2026-09-10T00:00:00Z'),
    row('9000000000000000000', '2026-09-02T00:00:00Z', '2026-09-05T00:00:00Z'),
    row('5000000000000000000', '2026-09-03T00:00:00Z', '2026-09-08T00:00:00Z'),
  ];

  it('best = highest amount first', () => {
    expect(sortOfferRows(rows, 'best').map((r) => r.amount_wei[0])).toEqual(['9', '5', '1']);
  });
  it('newest = latest created first', () => {
    expect(sortOfferRows(rows, 'newest')[0].created_at).toBe('2026-09-03T00:00:00Z');
  });
  it('expiring = soonest expiry first', () => {
    expect(sortOfferRows(rows, 'expiring')[0].expires_at).toBe('2026-09-05T00:00:00Z');
  });
  it('tolerates a garbage amount instead of throwing', () => {
    expect(() => sortOfferRows([row('not-a-number', '2026-09-01T00:00:00Z', '2026-09-02T00:00:00Z'), ...rows], 'best')).not.toThrow();
  });
});

describe('OffersPage expiry', () => {
  const now = new Date('2026-09-04T12:00:00Z').getTime();
  it('an offer past its expires_at is expired; a future one is not', () => {
    expect(isOfferExpired({ expires_at: '2026-09-04T11:59:59Z' }, now)).toBe(true);
    expect(isOfferExpired({ expires_at: '2026-09-06T00:00:00Z' }, now)).toBe(false);
  });
});
