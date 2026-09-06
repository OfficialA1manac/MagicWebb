// Home first-run strip + "Right now" zero-stats hiding + hero copy
// (spec B4 "Home") — pure exports from the component's module script.
import { describe, it, expect } from 'vitest';
import { rightNowLine, heroCopy } from './HomeSections.svelte';

const WEI = (n: number) => (BigInt(n) * 10n ** 18n).toString();

describe('"Right now" line', () => {
  it('hides itself (null) while every stat is zero — never renders zeros', () => {
    expect(rightNowLine({ listings: 0, liveAuctions: 0, offers: 0, soldTodayWei: '0' }, 'C2FLR')).toBeNull();
  });

  it('renders the sentence with counts and the sold-today amount', () => {
    expect(rightNowLine({ listings: 3, liveAuctions: 1, offers: 2, soldTodayWei: '0' }, 'C2FLR'))
      .toBe('3 listings · 1 live auction · 2 offers · 0 C2FLR sold today');
  });

  it('pluralises correctly', () => {
    const line = rightNowLine({ listings: 1, liveAuctions: 2, offers: 1, soldTodayWei: WEI(5) }, 'C2FLR')!;
    expect(line).toContain('1 listing ·');
    expect(line).toContain('2 live auctions');
    expect(line).toContain('1 offer ·');
    expect(line).toContain('5 C2FLR sold today');
  });

  it('shows when only volume is non-zero, and survives a garbage wei value', () => {
    expect(rightNowLine({ listings: 0, liveAuctions: 0, offers: 0, soldTodayWei: WEI(1) }, 'FLR')).toContain('sold today');
    expect(rightNowLine({ listings: 0, liveAuctions: 0, offers: 0, soldTodayWei: 'garbage' }, 'FLR')).toBeNull();
  });
});

describe('hero copy', () => {
  it('trading network: spec headline + sub; primary flips Connect → List when connected', () => {
    const viewer = heroCopy({ trading: true, networkName: 'Flare Coston2', connected: false });
    expect(viewer.headline).toBe('Buy and sell NFTs on Flare');
    expect(viewer.sub).toContain('Listing is free — you pay 2% only when something sells.');
    expect(viewer.primary).toEqual({ label: 'Connect wallet', kind: 'connect' });
    expect(viewer.secondary).toEqual({ label: 'Browse listings', href: '/listings' });
    expect(heroCopy({ trading: true, networkName: 'Flare Coston2', connected: true }).primary)
      .toEqual({ label: 'List an NFT', kind: 'list' });
  });

  it('browse-only network: Browse headline with the network name, Browse/Docs CTAs', () => {
    const h = heroCopy({ trading: false, networkName: 'Songbird', connected: false });
    expect(h.headline).toBe('Browse NFTs on Songbird');
    expect(h.primary.kind).toBe('browse');
    expect(h.secondary.href).toBe('/docs');
  });
});

// First-run strip helpers: see src/lib/firstrun.test.ts (moved in v3.6 wave 5c).
