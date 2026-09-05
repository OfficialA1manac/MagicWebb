// Bid-panel role matrix (spec B4 "Auctions" detail) — pure function, same
// pattern as TokenPage.actionZone: phase × role → cells, every disabled cell
// carries a Hint reason.
import { describe, it, expect } from 'vitest';
import { bidPanel } from './AuctionPage.svelte';

const kinds = (cells: ReturnType<typeof bidPanel>) => cells.map((c) => c.kind);

describe('AuctionPage bid panel matrix', () => {
  it('browse-only network trumps everything', () => {
    for (const role of ['viewer', 'buyer', 'seller'] as const) {
      for (const phase of ['live', 'ended', 'settled', 'cancelled'] as const) {
        expect(kinds(bidPanel({ phase, role, browseOnly: true }))).toEqual(['browse-only']);
      }
    }
  });

  // ── live ───────────────────────────────────────────────────────────────
  it('viewer → Connect wallet to bid + the minimum-bid hint', () => {
    const [c] = bidPanel({ phase: 'live', role: 'viewer', minLabel: '13 C2FLR' });
    expect(c.kind).toBe('bid-connect');
    expect(c.label).toBe('Connect wallet to bid');
    expect(c.hint).toBe('Minimum bid 13 C2FLR');
  });

  it('buyer (not leading) → bid input; with escrow also Withdraw', () => {
    expect(kinds(bidPanel({ phase: 'live', role: 'buyer' }))).toEqual(['bid']);
    const cells = bidPanel({ phase: 'live', role: 'buyer', held: true, heldLabel: '5 C2FLR' });
    expect(kinds(cells)).toEqual(['bid', 'withdraw-bid']);
    expect(cells[1].label).toBe('Withdraw my 5 C2FLR');
  });

  it('leader → the green leading card with the held amount', () => {
    const [c] = bidPanel({ phase: 'live', role: 'buyer', amLeader: true, heldLabel: '13 C2FLR' });
    expect(c.kind).toBe('leading');
    expect(c.label).toBe("You're the highest bidder with 13 C2FLR");
  });

  it('seller: cancel enabled with 0 bids, disabled + exact Hint copy with bids', () => {
    const noBids = bidPanel({ phase: 'live', role: 'seller', hasBids: false })[0];
    expect(noBids.kind).toBe('cancel-auction');
    expect(noBids.disabled).toBeFalsy();
    const withBids = bidPanel({ phase: 'live', role: 'seller', hasBids: true })[0];
    expect(withBids.disabled).toBe(true);
    expect(withBids.reason).toBe('Has bids — it will settle automatically at the end');
  });

  // ── ended ──────────────────────────────────────────────────────────────
  it('ended: seller and winner see Settle now', () => {
    expect(kinds(bidPanel({ phase: 'ended', role: 'seller' }))).toEqual(['settle']);
    expect(kinds(bidPanel({ phase: 'ended', role: 'buyer', isWinner: true, amLeader: true }))).toEqual(['settle']);
  });

  it('ended: bystander gets the settling copy (disabled + reason)', () => {
    const [c] = bidPanel({ phase: 'ended', role: 'viewer' });
    expect(c.kind).toBe('ended-info');
    expect(c.disabled).toBe(true);
    expect(c.reason).toBe('The NFT goes to the winner, the seller is paid minus 2%.');
  });

  it('ended 3+ days: "Cancel and refund everyone" appears for seller/winner', () => {
    expect(kinds(bidPanel({ phase: 'ended', role: 'seller', canForceCancel: true }))).toEqual(['settle', 'force-cancel']);
    const fc = bidPanel({ phase: 'ended', role: 'buyer', isWinner: true, amLeader: true, canForceCancel: true })[1];
    expect(fc.label).toBe('Cancel and refund everyone');
  });

  it('ended: an outbid buyer keeps Withdraw', () => {
    expect(kinds(bidPanel({ phase: 'ended', role: 'buyer', held: true }))).toEqual(['ended-info', 'withdraw-bid']);
  });

  // ── settled / cancelled ────────────────────────────────────────────────
  it('settled/cancelled: closed cell; escrow still withdrawable for losers', () => {
    expect(kinds(bidPanel({ phase: 'settled', role: 'viewer' }))).toEqual(['closed']);
    expect(kinds(bidPanel({ phase: 'cancelled', role: 'buyer', held: true }))).toEqual(['closed', 'withdraw-bid']);
    // The winner of a settled auction has nothing to withdraw.
    expect(kinds(bidPanel({ phase: 'settled', role: 'buyer', held: true, amLeader: true }))).toEqual(['closed']);
  });

  it('every cell in every combination is actionable or explains itself', () => {
    for (const role of ['viewer', 'buyer', 'seller'] as const) {
      for (const phase of ['live', 'ended', 'settled', 'cancelled'] as const) {
        for (const held of [false, true]) {
          const cells = bidPanel({ phase, role, held, hasBids: true, heldLabel: '1 C2FLR', minLabel: '2 C2FLR' });
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
