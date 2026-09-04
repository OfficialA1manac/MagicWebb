// Action-zone matrix (spec B4 "Token"): status × role, every cell either a
// visible control or a disabled control with a Hint reason. actionZone() is
// the exported single source the template and the mobile sticky bar follow.
import { describe, it, expect } from 'vitest';
import { actionZone } from './TokenPage.svelte';

const kinds = (cells: ReturnType<typeof actionZone>) => cells.map((c) => c.kind);

describe('TokenPage action zone matrix', () => {
  // ── browse-only network trumps everything ──────────────────────────────
  it('browse-only network → the browse-only card for every role/status', () => {
    for (const role of ['viewer', 'buyer', 'seller'] as const) {
      for (const status of ['not-listed', 'listed', 'auction-live', 'auction-ended'] as const) {
        expect(kinds(actionZone({ status, role, browseOnly: true }))).toEqual(['browse-only']);
      }
    }
  });

  // ── not listed ─────────────────────────────────────────────────────────
  it('not-listed / viewer → connect-to-offer prompt', () => {
    const [c] = actionZone({ status: 'not-listed', role: 'viewer', offersEligible: true });
    expect(c.kind).toBe('offer-connect');
    expect(c.label).toBe('Connect wallet to make an offer');
    expect(c.disabled).toBeUndefined();
  });

  it('not-listed / viewer with offers off → disabled + reason', () => {
    const [c] = actionZone({ status: 'not-listed', role: 'viewer', offersEligible: false });
    expect(c.disabled).toBe(true);
    expect(c.reason).toBe('Offers are off for this collection');
  });

  it('not-listed / buyer → Make offer (or Raise when an own offer exists)', () => {
    expect(actionZone({ status: 'not-listed', role: 'buyer', offersEligible: true })[0].kind).toBe('make-offer');
    expect(actionZone({ status: 'not-listed', role: 'buyer', offersEligible: true, hasOwnOffer: true })[0].kind).toBe('raise-offer');
  });

  it('not-listed / seller → the two equal free CTAs with exact copy', () => {
    const cells = actionZone({ status: 'not-listed', role: 'seller' });
    expect(cells.map((c) => c.label)).toEqual(['List for sale · free', 'Start auction · free']);
  });

  // ── listed ─────────────────────────────────────────────────────────────
  it('listed / viewer → Connect to buy with the exact fee hint', () => {
    const [c] = actionZone({ status: 'listed', role: 'viewer', priceLabel: '12.5 C2FLR' });
    expect(c.kind).toBe('buy-connect');
    expect(c.label).toContain('Connect to buy');
    expect(c.hint).toBe('You pay exactly this price. Seller pays the 2% fee.');
  });

  it('listed / buyer → Buy now with price + secondary Make offer', () => {
    const cells = actionZone({ status: 'listed', role: 'buyer', priceLabel: '12.5 C2FLR', offersEligible: true });
    expect(cells[0].label).toBe('Buy now · 12.5 C2FLR');
    expect(kinds(cells)).toEqual(['buy', 'make-offer']);
  });

  it('listed / seller → Change price + Cancel listing', () => {
    expect(kinds(actionZone({ status: 'listed', role: 'seller' }))).toEqual(['edit-price', 'cancel-listing']);
  });

  // ── auction live ───────────────────────────────────────────────────────
  it('auction-live / viewer → connect-to-bid prompt', () => {
    expect(kinds(actionZone({ status: 'auction-live', role: 'viewer' }))).toEqual(['bid-connect']);
  });

  it('auction-live / buyer → bid; outbid adds Withdraw right here', () => {
    expect(kinds(actionZone({ status: 'auction-live', role: 'buyer' }))).toEqual(['bid']);
    expect(kinds(actionZone({ status: 'auction-live', role: 'buyer', outbid: true }))).toEqual(['bid', 'withdraw-bid']);
  });

  it('auction-live / seller: cancel enabled without bids, disabled + reason with bids', () => {
    const noBids = actionZone({ status: 'auction-live', role: 'seller', hasBids: false })[0];
    expect(noBids.kind).toBe('cancel-auction');
    expect(noBids.disabled).toBeFalsy();
    const withBids = actionZone({ status: 'auction-live', role: 'seller', hasBids: true })[0];
    expect(withBids.disabled).toBe(true);
    expect(withBids.reason).toMatch(/cannot be cancelled/);
  });

  // ── auction ended ──────────────────────────────────────────────────────
  it('auction-ended / seller and winner → Settle now (+ force-cancel after 3 days)', () => {
    expect(kinds(actionZone({ status: 'auction-ended', role: 'seller' }))).toEqual(['settle']);
    expect(kinds(actionZone({ status: 'auction-ended', role: 'buyer', isWinner: true }))).toEqual(['settle']);
    expect(kinds(actionZone({ status: 'auction-ended', role: 'seller', canForceCancel: true }))).toEqual(['settle', 'force-cancel']);
  });

  it('auction-ended / losing buyer → info cell + Withdraw when escrowed', () => {
    const cells = actionZone({ status: 'auction-ended', role: 'buyer', outbid: true });
    expect(kinds(cells)).toEqual(['ended-info', 'withdraw-bid']);
    expect(cells[0].disabled).toBe(true);
    expect(cells[0].reason).toBeTruthy();
  });

  it('auction-ended / viewer → disabled info cell with reason (zone never empty)', () => {
    const [c] = actionZone({ status: 'auction-ended', role: 'viewer' });
    expect(c.disabled).toBe(true);
    expect(c.reason).toBeTruthy();
  });

  it('every cell in every combination is actionable or explains itself', () => {
    for (const role of ['viewer', 'buyer', 'seller'] as const) {
      for (const status of ['not-listed', 'listed', 'auction-live', 'auction-ended'] as const) {
        for (const offersEligible of [true, false, null]) {
          const cells = actionZone({ status, role, offersEligible, hasBids: true, outbid: true });
          expect(cells.length).toBeGreaterThan(0);
          for (const c of cells) {
            expect(c.label).toBeTruthy();
            if (c.disabled) expect(c.reason).toBeTruthy();
          }
        }
      }
    }
  });
});
