import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { currentChain, tradingLive, tradingOrigin, readOnlyCopy, networkOrigins, _resetChainCache } from './chains';

const w = globalThis as unknown as Record<string, unknown>;
const KEYS = ['MW_CHAIN_ID', 'MW_NETWORK_NAME', 'MW_MARKETPLACE', 'MW_AUCTION', 'MW_OFFERBOOK', 'MW_NETWORK_URLS'];

beforeEach(() => {
  _resetChainCache();
  for (const k of KEYS) delete w[k];
});
afterEach(() => {
  _resetChainCache();
  for (const k of KEYS) delete w[k];
});

describe('tradingLive', () => {
  it('is true when the marketplace address is injected', () => {
    w.MW_CHAIN_ID = '114';
    w.MW_MARKETPLACE = '0x49b3da6a0d5c31994c3a52d41416c6bc0a6200f3';
    expect(tradingLive()).toBe(true);
  });

  it('is false in read-only network mode (no contracts injected)', () => {
    w.MW_CHAIN_ID = '19';
    expect(currentChain().id).toBe(19);
    expect(tradingLive()).toBe(false);
  });

  it('rejects a malformed injected address', () => {
    w.MW_CHAIN_ID = '19';
    w.MW_MARKETPLACE = '0xNOTHEX';
    expect(tradingLive()).toBe(false);
  });
});

describe('readOnlyCopy', () => {
  it('names the current network and links to the Coston2 origin', () => {
    w.MW_CHAIN_ID = '19';
    w.MW_NETWORK_URLS = '114=https://magicwebb.fly.dev,19=https://magicwebb-songbird.fly.dev';
    const c = readOnlyCopy();
    expect(c.heading).toContain('Songbird');
    expect(c.ctaHref).toBe('https://magicwebb.fly.dev');
    expect(tradingOrigin()).toBe('https://magicwebb.fly.dev');
  });

  it('omits the CTA link when no Coston2 origin is known', () => {
    w.MW_CHAIN_ID = '14';
    expect(readOnlyCopy().ctaHref).toBeNull();
    expect(networkOrigins().size).toBe(0);
  });
});
