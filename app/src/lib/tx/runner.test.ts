import { describe, it, expect, vi, beforeEach } from 'vitest';

// ── mocks (hoisted) ────────────────────────────────────────────────────────
const wagmi = vi.hoisted(() => ({
  switchChain: vi.fn(),
  getWalletClient: vi.fn(),
  getAccount: vi.fn(),
  getPublicClient: vi.fn(),
  watchAccount: vi.fn(),
}));
vi.mock('@wagmi/core', () => wagmi);

const wsMock = vi.hoisted(() => ({ waitFor: vi.fn(() => Promise.reject(new Error('timeout'))) }));
vi.mock('../ws/client', () => ({ ws: wsMock }));

import { runTx, estimateFee, type TxPlan } from './runner';
import { txModal, runWithModal } from '../stores/txmodal.svelte';
import { toasts, clearToasts } from '../toast.svelte';
import { TxError } from './errors';
import { switchToSiteChain, isUnrecognizedChainError, addChainParams } from './client';
import { _resetChainCache } from '../chains';

const HASH = '0x' + 'ab'.repeat(32);
const ADDR = '0x1111111111111111111111111111111111111111' as const;

function makeCtx(over: Partial<{ estimateGas: bigint; gasPrice: bigint; simulateError: unknown; receiptStatus: 'success' | 'reverted' }> = {}) {
  const pub = {
    simulateContract: vi.fn(async (req: unknown) => { if (over.simulateError) throw over.simulateError; return { request: req }; }),
    waitForTransactionReceipt: vi.fn(async () => ({ status: over.receiptStatus ?? 'success', transactionHash: HASH })),
    estimateContractGas: vi.fn(async () => over.estimateGas ?? 21_000n),
    getGasPrice: vi.fn(async () => over.gasPrice ?? 25_000_000_000n),
  };
  const wallet = { writeContract: vi.fn(async () => HASH) };
  return { config: {} as never, account: ADDR, pub, wallet } as never;
}

const plan = (extra: Partial<TxPlan> = {}): TxPlan => ({
  title: 'Buy Animi #42',
  request: async () => ({ address: ADDR, abi: [], functionName: 'buy', args: [] }),
  ...extra,
});

beforeEach(() => {
  vi.restoreAllMocks();
  clearToasts();
  Object.assign(txModal, { open: false, title: '', summary: [], step: 'idle', hasApproval: false, success: undefined, feeEstimate: undefined, error: undefined });
  window.__MW_WAGMI_CONFIG__ = {} as never;
  window.MW_CHAIN_ID = '114';
  _resetChainCache();
  // requireWallet(): connected on the right chain, viem clients from makeCtx.
  wagmi.getAccount.mockReturnValue({ isConnected: true, address: ADDR, chainId: 114 });
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({}) })));
});

function wireWallet(ctx: ReturnType<typeof makeCtx>) {
  const c = ctx as unknown as { pub: unknown; wallet: unknown };
  wagmi.getWalletClient.mockResolvedValue(c.wallet);
  wagmi.getPublicClient.mockReturnValue(c.pub);
}

describe('runTx — summary, success card, toasts', () => {
  it('copies plan.summary onto the txmodal store before signing', async () => {
    const ctx = makeCtx(); wireWallet(ctx);
    const rows: Array<[string, string]> = [['You pay', '12.5 C2FLR'], ['Seller receives', '12.25 C2FLR (2% fee)'], ['You get', 'The NFT instantly']];
    await runTx(plan({ summary: rows }), {}, { observe: false });
    expect(txModal.summary).toEqual(rows);
  });

  it('mw-style runWithModal + runTx: the plan rows win over the empty page fallback', async () => {
    const ctx = makeCtx(); wireWallet(ctx);
    const rows: Array<[string, string]> = [['Price', '5 C2FLR']];
    await runWithModal({ title: 'List #1', summary: undefined, hasApproval: false }, (hooks) => runTx(plan({ title: 'List #1', summary: rows }), hooks, { observe: false }));
    expect(txModal.summary).toEqual(rows);
    expect(txModal.step).toBe('confirmed');
  });

  it('sets the success card from plan.success and fires a success toast', async () => {
    const ctx = makeCtx(); wireWallet(ctx);
    const success = { message: 'You bought Animi #42', action: { label: 'View in your profile', href: '/profile' } };
    const steps: string[] = [];
    const res = await runTx(plan({ success }), { onStep: (s) => steps.push(s) }, { observe: false });
    expect(res.hash).toBe(HASH);
    expect(steps).toEqual(['sign', 'pending', 'confirmed']);
    expect(txModal.success).toEqual(success);
    expect(toasts.map((t) => [t.variant, t.message, t.action?.href])).toEqual([['success', 'You bought Animi #42', '/profile']]);
  });

  it('fires an error toast on failure (not for a cancelled wallet prompt)', async () => {
    const ctx = makeCtx({ simulateError: new TxError('InsufficientFunds', 'Not enough C2FLR to cover the amount plus gas.') }); wireWallet(ctx);
    await expect(runTx(plan(), {}, { observe: false })).rejects.toMatchObject({ kind: 'InsufficientFunds' });
    expect(toasts.map((t) => [t.variant, t.message])).toEqual([['error', 'Not enough C2FLR to cover the amount plus gas.']]);

    clearToasts();
    const ctx2 = makeCtx({ simulateError: new TxError('UserRejected', 'You rejected the request in your wallet.') }); wireWallet(ctx2);
    await expect(runTx(plan(), {}, { observe: false })).rejects.toMatchObject({ kind: 'UserRejected' });
    expect(toasts).toHaveLength(0);
  });

  it('step labels follow the B3 rail copy', async () => {
    const ctx = makeCtx(); wireWallet(ctx);
    const labels: Record<string, string> = {};
    await runTx(plan({ approval: async () => ({ address: ADDR, abi: [], functionName: 'setApprovalForAll', args: [] }) }), { onStep: (s, m) => { labels[s] = m.label; } }, { observe: false });
    expect(labels.approve).toBe('Allow MagicWebb to move this NFT (one time)');
    expect(labels.sign).toBe('Confirm in your wallet');
    expect(labels.pending).toBe('Waiting for Flare Coston2 (~3s)');
    expect(labels.confirmed).toBe('Done');
  });
});

describe('estimateFee', () => {
  it('estimateGas × gasPrice → "≈ x SYMBOL"', async () => {
    const ctx = makeCtx({ estimateGas: 200_000n, gasPrice: 100_000_000_000n }); // 0.02 native
    expect(await estimateFee(ctx, { address: ADDR, abi: [], functionName: 'buy', args: [] })).toBe('≈ 0.02 C2FLR');
  });

  it('returns undefined (hidden) when estimation fails', async () => {
    const ctx = makeCtx();
    (ctx as unknown as { pub: { estimateContractGas: ReturnType<typeof vi.fn> } }).pub.estimateContractGas.mockRejectedValue(new Error('nope'));
    expect(await estimateFee(ctx, { address: ADDR, abi: [], functionName: 'buy', args: [] })).toBeUndefined();
  });

  it('runTx stores the estimate on the modal (best effort)', async () => {
    const ctx = makeCtx({ estimateGas: 200_000n, gasPrice: 100_000_000_000n }); wireWallet(ctx);
    await runTx(plan(), {}, { observe: false });
    await Promise.resolve();
    expect(txModal.feeEstimate).toBe('≈ 0.02 C2FLR');
  });
});

describe('switchToSiteChain — 4902 → wallet_addEthereumChain', () => {
  it('recognises 4902 at any depth of cause', () => {
    expect(isUnrecognizedChainError({ code: 4902 })).toBe(true);
    expect(isUnrecognizedChainError({ cause: { cause: { code: 4902 } } })).toBe(true);
    expect(isUnrecognizedChainError({ message: 'Unrecognized chain ID "0x72"' })).toBe(true);
    expect(isUnrecognizedChainError({ code: 4001 })).toBe(false);
    expect(isUnrecognizedChainError(new Error('User rejected'))).toBe(false);
  });

  it('adds the chain with the params from chains.ts, then switches again', async () => {
    const request = vi.fn(async () => null);
    wagmi.switchChain.mockRejectedValueOnce({ code: 4902 }).mockResolvedValueOnce(undefined);
    wagmi.getWalletClient.mockResolvedValue({ request });
    await switchToSiteChain({} as never);
    expect(request).toHaveBeenCalledWith({ method: 'wallet_addEthereumChain', params: [addChainParams()] });
    expect(request.mock.calls[0][0].params[0]).toMatchObject({ chainId: '0x72', chainName: 'Flare Coston2', nativeCurrency: { symbol: 'C2FLR', decimals: 18 }, rpcUrls: ['https://coston2-api.flare.network/ext/C/rpc'] });
    expect(wagmi.switchChain).toHaveBeenCalledTimes(2);
  });

  it('rethrows non-4902 errors without adding a chain', async () => {
    const request = vi.fn();
    wagmi.switchChain.mockRejectedValueOnce({ code: 4001, message: 'User rejected' });
    wagmi.getWalletClient.mockResolvedValue({ request });
    await expect(switchToSiteChain({} as never)).rejects.toMatchObject({ code: 4001 });
    expect(request).not.toHaveBeenCalled();
  });

  it('requireWallet uses the same fallback and reports WrongChain when the user declines', async () => {
    wagmi.getAccount.mockReturnValue({ isConnected: true, address: ADDR, chainId: 14 });
    wagmi.switchChain.mockRejectedValueOnce({ code: 4902 });
    wagmi.getWalletClient.mockResolvedValue({ request: vi.fn(async () => { throw { code: 4001 }; }) });
    await expect(runTx(plan(), {}, { observe: false })).rejects.toMatchObject({ kind: 'WrongChain' });
    expect(txModal.error).toBeUndefined(); // no modal wired here; toast carries it
    expect(toasts[0]?.variant).toBe('error');
  });
});
