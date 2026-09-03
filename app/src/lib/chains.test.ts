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

const MP = '0x49b3da6a0d5c31994c3a52d41416c6bc0a6200f3';
const AH = '0x93627ca0032bc1b8ee0d9134a7c9c4f74d97d04c';
const OB = '0x918bf8748c9950dc6adbb2796de6dffaf3f44d5e';

describe('tradingLive', () => {
  it('is true when all three core addresses are injected', () => {
    w.MW_CHAIN_ID = '114';
    w.MW_MARKETPLACE = MP; w.MW_AUCTION = AH; w.MW_OFFERBOOK = OB;
    expect(tradingLive()).toBe(true);
  });

  it('is NOT live with only the marketplace address (partial config must not light up auction/offer CTAs)', () => {
    w.MW_CHAIN_ID = '114';
    w.MW_MARKETPLACE = MP;
    expect(currentChain().contracts.marketplace).not.toBeNull();
    expect(tradingLive()).toBe(false);
  });

  it('is NOT live when any one of the three is missing', () => {
    w.MW_CHAIN_ID = '114';
    w.MW_MARKETPLACE = MP; w.MW_AUCTION = AH;
    expect(tradingLive()).toBe(false);
    _resetChainCache();
    w.MW_OFFERBOOK = OB; delete w.MW_AUCTION;
    expect(tradingLive()).toBe(false);
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
  it('names the current network and links to the preferred sibling trading origin', () => {
    w.MW_CHAIN_ID = '19';
    w.MW_NETWORK_URLS = '114=https://magicwebb.fly.dev,19=https://magicwebb-songbird.fly.dev';
    const c = readOnlyCopy();
    expect(c.heading).toContain('Songbird');
    expect(c.ctaHref).toBe('https://magicwebb.fly.dev');
    expect(tradingOrigin()).toBe('https://magicwebb.fly.dev');
  });

  it('omits the CTA link when no sibling origin is known', () => {
    w.MW_CHAIN_ID = '14';
    expect(readOnlyCopy().ctaHref).toBeNull();
    expect(networkOrigins().size).toBe(0);
  });
});

describe('tradingOrigin', () => {
  const URLS = '114=https://c2.example,14=https://flare.example,19=https://sgb.example';

  it('prefers 114, then 14, then 19', () => {
    w.MW_CHAIN_ID = '19'; w.MW_NETWORK_URLS = URLS;
    expect(tradingOrigin()).toBe('https://c2.example');
    _resetChainCache();
    w.MW_NETWORK_URLS = '14=https://flare.example,19=https://sgb.example';
    expect(tradingOrigin()).toBe('https://flare.example');
  });

  it('never points at the current network (self-exclusion)', () => {
    w.MW_CHAIN_ID = '114'; w.MW_NETWORK_URLS = URLS;
    expect(tradingOrigin()).toBe('https://flare.example');
    _resetChainCache();
    w.MW_CHAIN_ID = '14';
    expect(tradingOrigin()).toBe('https://c2.example');
    _resetChainCache();
    w.MW_CHAIN_ID = '14'; w.MW_NETWORK_URLS = '14=https://flare.example,19=https://sgb.example';
    expect(tradingOrigin()).toBe('https://sgb.example');
  });

  it('is null when only the current network has an origin', () => {
    w.MW_CHAIN_ID = '19'; w.MW_NETWORK_URLS = '19=https://sgb.example';
    expect(tradingOrigin()).toBeNull();
    expect(readOnlyCopy().ctaHref).toBeNull();
  });

  it('skips unknown ids and malformed entries', () => {
    w.MW_CHAIN_ID = '19'; w.MW_NETWORK_URLS = '999=https://nope.example,garbage,14=https://flare.example';
    expect(tradingOrigin()).toBe('https://flare.example');
  });
});
