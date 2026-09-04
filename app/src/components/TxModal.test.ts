import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { screen, fireEvent, within } from '@testing-library/dom';

const wagmi = vi.hoisted(() => ({ switchChain: vi.fn(), getWalletClient: vi.fn(), getAccount: vi.fn(), getPublicClient: vi.fn(), watchAccount: vi.fn() }));
vi.mock('@wagmi/core', () => wagmi);

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

import TxModal from './TxModal.svelte';
import { txModal, resetTxModal } from '../lib/stores/txmodal.svelte';
import { TxError } from '../lib/tx/errors';
import { _resetChainCache } from '../lib/chains';

let app: ReturnType<typeof mount> | undefined;
let host: HTMLDivElement;

beforeEach(() => {
  window.MW_CHAIN_ID = '114';
  window.MW_FAUCET_URL = 'https://faucet.example/coston2';
  _resetChainCache();
  resetTxModal();
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(TxModal, { target: host });
});
afterEach(() => { if (app) unmount(app); host.remove(); resetTxModal(); });

function open(patch: Partial<typeof txModal>) {
  Object.assign(txModal, { open: true, title: 'Buy Animi #42', summary: [], hasApproval: false, step: 'sign', ...patch });
  flushSync();
}

describe('TxModal — summary before the rail', () => {
  it('renders the plan rows in a <dl> placed before the step rail', () => {
    open({ summary: [['You pay', '12.5 C2FLR'], ['Seller receives', '12.25 C2FLR (2% fee)'], ['You get', 'The NFT instantly']] });
    const dialog = screen.getByRole('dialog');
    const dl = dialog.querySelector('dl.mw-tx-summary')!;
    const rail = dialog.querySelector('ol.mw-tx-rail')!;
    expect(dl).toBeTruthy();
    expect(dl.compareDocumentPosition(rail) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const dts = [...dl.querySelectorAll('dt')].map((d) => d.textContent);
    const dds = [...dl.querySelectorAll('dd')].map((d) => d.textContent);
    expect(dts).toEqual(['You pay', 'Seller receives', 'You get']);
    expect(dds).toEqual(['12.5 C2FLR', '12.25 C2FLR (2% fee)', 'The NFT instantly']);
  });

  it('rail copy follows B3 (approval row only when the plan has one)', () => {
    open({ hasApproval: true, step: 'approve' });
    const items = [...screen.getByRole('dialog').querySelectorAll('.mw-tx-step-label')].map((l) => l.textContent?.trim());
    expect(items).toEqual(['Allow MagicWebb to move this NFT (one time)', 'Confirm in your wallet', 'Waiting for Flare Coston2 (~3s)', 'Done']);
  });

  it('shows the estimated network fee row while pending and hides it when done', () => {
    open({ summary: [['Your bid', '13 C2FLR']], feeEstimate: '≈ 0.02 C2FLR', step: 'pending' });
    expect(screen.getByText('Network fee')).toBeTruthy();
    expect(screen.getByText('≈ 0.02 C2FLR')).toBeTruthy();
    open({ summary: [['Your bid', '13 C2FLR']], feeEstimate: '≈ 0.02 C2FLR', step: 'confirmed' });
    expect(screen.queryByText('Network fee')).toBeNull();
  });
});

describe('TxModal — success card, one CTA per flow', () => {
  it.each([
    ['Listed!', 'View listing', '/token/0xabc/1'],
    ['You bought Animi #42', 'View in your profile', '/profile'],
    ['Offer sent', 'See your offers', '/offers'],
    ['Bid placed', 'Watch the auction', '/auction/7'],
  ])('%s → [%s]', (message, label, href) => {
    open({ step: 'confirmed', success: { message, action: { label, href } }, successAction: { label: 'page fallback', href: '/x' } });
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByRole('status').textContent).toContain(message);
    const links = within(dialog).getAllByRole('link');
    const primary = links.find((a) => a.classList.contains('mw-btn-primary'))!;
    expect(primary.textContent).toBe(label);
    expect(primary.getAttribute('href')).toBe(href);
    expect(within(dialog).queryByText('page fallback')).toBeNull();
    expect(within(dialog).getByRole('button', { name: 'Done' })).toBeTruthy();
  });

  it('falls back to the page-supplied CTA when the plan has none', () => {
    open({ step: 'indexed', success: { message: 'Auction settled' }, successAction: { label: 'See live auctions', href: '/auctions' } });
    expect(screen.getByRole('link', { name: 'See live auctions' }).getAttribute('href')).toBe('/auctions');
  });
});

describe('TxModal — errors', () => {
  it('wrong network: [Switch to Coston2] calls switchChain, adds the chain on 4902, then retries', async () => {
    const retry = vi.fn();
    const request = vi.fn(async () => null);
    window.__MW_WAGMI_CONFIG__ = {} as never;
    wagmi.switchChain.mockRejectedValueOnce({ code: 4902 }).mockResolvedValueOnce(undefined);
    wagmi.getWalletClient.mockResolvedValue({ request });
    open({ step: 'error', error: new TxError('WrongChain', 'Your wallet is on a different network.'), retry });
    expect(screen.getByText('Wrong network')).toBeTruthy();
    const btn = screen.getByRole('button', { name: 'Switch to Flare Coston2' });
    await fireEvent.click(btn);
    await vi.waitFor(() => expect(retry).toHaveBeenCalled());
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ method: 'wallet_addEthereumChain' }));
  });

  it('not enough funds on chain 114: [Get test FLR] links to MW_FAUCET_URL', () => {
    open({ step: 'error', error: new TxError('InsufficientFunds', 'You need 12.5 C2FLR + gas.') });
    expect(screen.getByText('Not enough funds')).toBeTruthy();
    expect(screen.getByRole('link', { name: /Get test FLR/ }).getAttribute('href')).toBe('https://faucet.example/coston2');
  });

  it('other failures offer Copy details', async () => {
    const write = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: write }, configurable: true });
    open({ step: 'error', error: new TxError('ContractRevert', 'The blockchain rejected this transaction.', { revertName: 'NotListed' }), hash: '0xdeadbeef' as never });
    expect(screen.getByText('Could not complete')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Copy details' }));
    await vi.waitFor(() => expect(write).toHaveBeenCalled());
    expect(String(write.mock.calls[0][0])).toContain('NotListed');
    expect(String(write.mock.calls[0][0])).toContain('0xdeadbeef');
  });

  it('cancelled prompt is titled Cancelled', () => {
    open({ step: 'error', error: new TxError('UserRejected', 'You rejected the request in your wallet.') });
    expect(screen.getByText('Cancelled')).toBeTruthy();
  });
});
