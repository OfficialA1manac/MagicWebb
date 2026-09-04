import { describe, it, expect, afterEach } from 'vitest';
import { badgeTier, badgeHtml, badgeTip, mwBadgeGlobal, BADGE_HREF } from './badge';

const CREATOR = '0x871f5D3C3aE1E4B2b8F0c9A7d6E5F4a3B2C1a558';

describe('badgeTier', () => {
  it.each([
    // verified, creator, tracked → tier
    [true, CREATOR, true, 'authentic'],
    [true, CREATOR, undefined, 'authentic'],
    [true, '', true, 'verified'],
    [true, undefined, undefined, 'verified'],
    [true, '0x0000000000000000000000000000000000000000', true, 'verified'], // zero address is "no creator"
    [true, 'not-an-address', true, 'verified'],
    [false, '', true, 'tracked'],
    [false, CREATOR, true, 'tracked'], // creator alone never upgrades an unverified collection
    [false, '', undefined, 'tracked'], // tracked rows always carry a boolean verified flag
    [false, '', false, null],
    [undefined, '', undefined, null], // untracked: no flag at all
    [undefined, CREATOR, false, null],
  ] as const)('verified=%s creator=%s tracked=%s → %s', (verified, creator, tracked, tier) => {
    expect(badgeTier({ collection_verified: verified, collection_creator: creator, collection_tracked: tracked })).toBe(tier);
  });

  it('null/undefined row → null', () => {
    expect(badgeTier(null)).toBeNull();
    expect(badgeTier(undefined)).toBeNull();
  });
});

describe('badgeHtml markup contract', () => {
  it('renders the exact .vb markup for each tier (span by default)', () => {
    expect(badgeHtml({ collection_verified: true, collection_creator: '' }))
      .toBe('<span class="vb is-ok sm" title="Standard NFT contract and metadata confirmed. Not a judgement of the art or the seller." aria-label="Standard NFT contract and metadata confirmed. Not a judgement of the art or the seller."><span class="vb-dot" aria-hidden="true">✓</span>Verified</span>');
    expect(badgeHtml({ collection_verified: false, collection_tracked: true }))
      .toContain('class="vb is-tracked sm"');
    expect(badgeHtml({ collection_verified: false, collection_tracked: true })).toContain('>Listed collection</span>');
    expect(badgeHtml({ collection_verified: true, collection_creator: CREATOR })).toContain('class="vb is-authentic sm"');
    expect(badgeHtml({ collection_verified: true, collection_creator: CREATOR })).toContain('>Authentic</span>');
  });

  it('authentic tooltip names the short creator address', () => {
    const tip = badgeTip('authentic', { collection_verified: true, collection_creator: CREATOR });
    expect(tip).toBe('Verified, and the creator is known: 0x871f…a558.');
    expect(badgeHtml({ collection_verified: true, collection_creator: CREATOR })).toContain(`title="${tip}"`);
  });

  it('link:true renders an <a href="/docs/faq#verified">', () => {
    const html = badgeHtml({ collection_verified: true }, { link: true, size: 'md' });
    expect(html.startsWith(`<a class="vb is-ok md" href="${BADGE_HREF}"`)).toBe(true);
    expect(html.endsWith('</a>')).toBe(true);
  });

  it('untracked → empty string (no pill)', () => {
    expect(badgeHtml({})).toBe('');
    expect(badgeHtml({ collection_verified: false, collection_tracked: false })).toBe('');
  });

  it('escapes the inline style and tooltip', () => {
    const html = badgeHtml({ collection_verified: true }, { style: 'top:0.5rem;"onmouseover="x' });
    expect(html).toContain('style="top:0.5rem;&quot;onmouseover=&quot;x"');
    expect(html).not.toContain('"onmouseover="');
  });
});

describe('mwBadgeGlobal', () => {
  afterEach(() => { delete window.mwVerifiedBadge; });

  it('installs window.mwVerifiedBadge with the inline-script signature', () => {
    mwBadgeGlobal();
    expect(typeof window.mwVerifiedBadge).toBe('function');
    const fn = window.mwVerifiedBadge!;
    expect(fn(true, CREATOR, 'Animi', 'position:absolute;')).toContain('is-authentic');
    expect(fn(true, CREATOR, 'Animi', 'position:absolute;')).toContain('style="position:absolute;"');
    expect(fn(true, '', '', '')).toContain('is-ok');
    expect(fn(false, '', '', '', true)).toContain('is-tracked');
    // boolean verified with no tracked flag = tracked row (grey pill)
    expect(fn(false, '', '', '')).toContain('is-tracked');
    // non-boolean verified with no tracked flag = untracked (no pill)
    expect(fn(undefined, '', '', '')).toBe('');
    expect(fn(false, '', '', '', false)).toBe('');
  });
});
