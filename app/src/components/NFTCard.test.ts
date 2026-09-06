import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { within } from '@testing-library/dom';
// jsdom has no Web Animations API; Svelte transitions call element.animate().
// Finish every animation on the next microtask so intro/outro resolve.
if (typeof Element.prototype.animate !== 'function') {
  Element.prototype.animate = function () {
    const a = { cancel() {}, pause() {}, play() {}, finish() {}, currentTime: 0, playState: 'finished', finished: Promise.resolve(), oncancel: null as null | (() => void) };
    let fin: null | (() => void) = null;
    Object.defineProperty(a, 'onfinish', { get: () => fin, set(fn: () => void) { fin = fn; queueMicrotask(() => fn?.()); } });
    return a as unknown as Animation;
  };
}

import NFTCard from './NFTCard.svelte';

const CREATOR = '0x871f5D3C3aE1E4B2b8F0c9A7d6E5F4a3B2C1a558';
const SELLER = '0x2222222222222222222222222222222222222222';

const base = {
  collection: '0x3333333333333333333333333333333333333333',
  token_id: '42',
  seller: SELLER,
  price_wei: '12500000000000000000',
  amount: 1,
  standard: 'erc721',
  expires_at: '', listed_at: '', tx_hash: '',
  name: 'Animi #42',
  image_uri: '',
  total_supply: 100,
  collection_verified: true,
  collection_creator: '',
  collection_name: 'Magic Webb Animi',
};

let host: HTMLDivElement;
let app: ReturnType<typeof mount> | undefined;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); });
afterEach(() => { if (app) unmount(app); app = undefined; host.remove(); });

function render(item: Partial<typeof base> & Record<string, unknown>) {
  app = mount(NFTCard, { target: host, props: { item: { ...base, ...item } as never } });
  flushSync();
  return within(host);
}

describe('NFTCard badges (v3.6: NFT-level ✓ + creator-held-and-minted ★)', () => {
  it('verified NFT → ✓ span with the check tooltip (never a nested <a>)', () => {
    const q = render({ verified: true, verified_reason: [] });
    const vb = host.querySelector('.vb.vb-check')!;
    expect(vb.tagName).toBe('SPAN');
    expect(vb.getAttribute('title')).toContain('Verified NFT');
    expect(host.querySelectorAll('a').length).toBe(1); // only the card link
    expect(q.queryByText('Creator')).toBeNull();
    // The tap-able explanation lives outside the anchor.
    expect(host.querySelector('a .hint')).toBeNull();
    expect(host.querySelector('.card-hint .hint-btn')).toBeTruthy();
  });

  it('unverified NFT → no checkmark and no hint button', () => {
    render({ verified: false, verified_reason: ['image_missing'] });
    expect(host.querySelector('.vb.vb-check')).toBeNull();
    expect(host.querySelector('.card-hint')).toBeNull();
  });

  it('no backend flag → client-side rule (verified collection + name + stored image)', () => {
    render({ verified: undefined, collection_verified: true, name: 'A', image_uri: '/api/v1/img/x' });
    expect(host.querySelector('.vb.vb-check')).toBeTruthy();
    render({ verified: undefined, collection_verified: true, name: 'A', image_uri: 'ipfs://x' });
    expect(host.querySelectorAll('.vb.vb-check').length).toBe(1); // only the first render's card
  });

  it('★ Creator only when the backend says creator_is_owner — seller == creator alone is not enough', () => {
    render({ collection_creator: CREATOR, seller: CREATOR.toLowerCase(), verified: true });
    expect(host.querySelector('.vb.is-creator')).toBeNull();
    render({ collection_creator: CREATOR, seller: CREATOR.toLowerCase(), verified: true, creator_is_owner: true });
    const c = host.querySelector('.vb.is-creator')!;
    expect(c.textContent).toContain('Creator');
    expect(c.getAttribute('title')).toContain('creator');
  });
});

describe('NFTCard edition chip', () => {
  it('erc721 → "1 of 1"', () => {
    const q = render({ standard: 'erc721' });
    expect(q.getByText('1 of 1')).toBeTruthy();
    expect(q.queryByText(/erc721/i)).toBeNull();
  });

  it('erc1155 → "Multi-edition" and the xN amount', () => {
    const q = render({ standard: 'erc1155', amount: 5 });
    expect(q.getByText('Multi-edition')).toBeTruthy();
    expect(q.getByText('x5')).toBeTruthy();
    expect(q.queryByText(/erc1155/i)).toBeNull();
  });
});
