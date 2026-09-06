import { describe, it, expect } from 'vitest';
import { firstRunSteps, stripState } from './firstrun';

describe('first-run steps', () => {
  it('three steps; step 1 checks off when connected and drops the no-wallet affordance', () => {
    const steps = firstRunSteps({ connected: true, testnet: true, faucetUrl: 'https://faucet.flare.network/coston2' });
    expect(steps).toHaveLength(3);
    expect(steps[0]).toMatchObject({ n: 1, done: true, noWallet: false });
    expect(firstRunSteps({ connected: false, testnet: true })[0]).toMatchObject({ done: false, noWallet: true });
  });

  it('step 2 is the faucet link on testnets, funding copy on mainnets', () => {
    const testnet = firstRunSteps({ connected: false, testnet: true, faucetUrl: 'https://faucet.flare.network/coston2' })[1];
    expect(testnet.label).toBe('Get free test FLR');
    expect(testnet.href).toBe('https://faucet.flare.network/coston2');
    const mainnet = firstRunSteps({ connected: false, testnet: false })[1];
    expect(mainnet.label).toBe('Fund your wallet with FLR');
    expect(mainnet.href).toBeUndefined();
  });

  it('step 3 checks off after the first trade', () => {
    expect(firstRunSteps({ connected: true, testnet: true, traded: true })[2]).toMatchObject({ n: 3, done: true });
  });
});

describe('strip state table', () => {
  it('fresh: visible, step 1 highlighted, no dismiss', () => {
    expect(stripState({ dismissed: false, traded: false, connected: false })).toEqual({ visible: true, dismissible: false, current: 1, mode: 'fresh' });
  });
  it('progress: connected → step 3 highlighted, still no dismiss', () => {
    expect(stripState({ dismissed: false, traded: false, connected: true })).toEqual({ visible: true, dismissible: false, current: 3, mode: 'progress' });
  });
  it('done: traded → "You\'re set" with Dismiss', () => {
    expect(stripState({ dismissed: false, traded: true })).toEqual({ visible: true, dismissible: true, current: 0, mode: 'done' });
  });
  it('hidden: dismissed wins over everything', () => {
    expect(stripState({ dismissed: true, traded: true, connected: true }).mode).toBe('hidden');
    expect(stripState({ dismissed: true, traded: false }).visible).toBe(false);
  });
});
