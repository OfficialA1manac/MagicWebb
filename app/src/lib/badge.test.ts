import { describe, it, expect } from 'vitest';
import { badgeTier, badgeTip, tokenCheck, showCreatorOnItem, REASON_COPY, CHECK_TIP, BADGE_HREF, CREATOR_HREF } from './badge';

const CREATOR = '0x871f5D3C3aE1E4B2b8F0c9A7d6E5F4a3B2C1a558';

describe('badgeTier (collection pill)', () => {
  it.each([
    [true, CREATOR, true, 'authentic'],
    [true, CREATOR, undefined, 'authentic'],
    [true, '', true, 'verified'],
    [true, undefined, undefined, 'verified'],
    [true, '0x0000000000000000000000000000000000000000', true, 'verified'],
    [true, 'not-an-address', true, 'verified'],
    [false, '', true, 'tracked'],
    [false, CREATOR, true, 'tracked'],
    [false, '', undefined, 'tracked'],
    [false, '', false, null],
    [undefined, '', undefined, null],
    [undefined, CREATOR, false, null],
  ] as const)('verified=%s creator=%s tracked=%s → %s', (verified, creator, tracked, tier) => {
    expect(badgeTier({ collection_verified: verified, collection_creator: creator, collection_tracked: tracked })).toBe(tier);
  });

  it('null/undefined row → null; tips per tier', () => {
    expect(badgeTier(null)).toBeNull();
    expect(badgeTip('authentic', { collection_creator: CREATOR })).toContain('creator is known');
    expect(badgeTip('tracked')).toContain('still being checked');
    expect(badgeTip(null)).toBe('');
  });
});

describe('tokenCheck (NFT-level ✓)', () => {
  it('trusts the backend flag and reasons when present', () => {
    expect(tokenCheck({ verified: true, verified_reason: [] })).toEqual({ ok: true, reasons: [], tip: CHECK_TIP });
    const r = tokenCheck({ verified: false, verified_reason: ['image_missing', 'owner_unknown'] });
    expect(r.ok).toBe(false);
    expect(r.tip).toContain(REASON_COPY.image_missing);
    expect(r.tip).toContain(REASON_COPY.owner_unknown);
  });

  it('falls back to the backend rule when the flag is absent', () => {
    expect(tokenCheck({ collection_verified: true, name: 'A', image_uri: '/api/v1/img/x' }).ok).toBe(true);
    expect(tokenCheck({ collection_verified: true, name: 'A', image: 'data:image/svg+xml;base64,AA' }).ok).toBe(true);
    expect(tokenCheck({ collection_verified: true, name: 'A', image_uri: 'ipfs://x' }).reasons).toEqual(['image_missing']);
    expect(tokenCheck({ collection_verified: false, name: '', image_uri: '' }).reasons).toEqual(['unverified_collection', 'no_metadata', 'image_missing']);
    expect(tokenCheck({ collection_verified: true, name: 'A', image_uri: '/img/x', owner: '' }).reasons).toEqual(['owner_unknown']);
  });

  it('null row → not ok, empty tip', () => {
    expect(tokenCheck(null)).toEqual({ ok: false, reasons: [], tip: '' });
  });
});

describe('★ Creator on items', () => {
  it('only the backend creator_is_owner flag shows it — never seller == creator alone', () => {
    expect(showCreatorOnItem({ creator_is_owner: true })).toBe(true);
    expect(showCreatorOnItem({ collection_creator: CREATOR, seller: CREATOR })).toBe(false);
    expect(showCreatorOnItem(null)).toBe(false);
  });
  it('FAQ anchors exist for both badges', () => {
    expect(BADGE_HREF).toBe('/docs/faq#verified');
    expect(CREATOR_HREF).toBe('/docs/faq#creator');
  });
});
