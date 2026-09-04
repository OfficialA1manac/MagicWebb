// The one state machine every transaction passes through. TxModal renders
// `TxState`; pages call `runTx()` and react to `onStep`.
//
//   idle → approve? → sign → pending → confirmed → indexed
//                                    ↘ error (any step)
import type { Abi, Address, Hex, TransactionReceipt } from 'viem';
import { currentChain, explorerTx } from '../chains';
import { requireWallet, type WalletCtx } from './client';
import { decodeRevert, TxError } from './errors';
import { ws } from '../ws/client';
import { txChannel } from '../ws/channels';
import { txModal } from '../stores/txmodal.svelte';
import { toastError, toastSuccess } from '../toast.svelte';
import { fmtPrice } from '../format';

export type TxStep = 'idle' | 'approve' | 'sign' | 'pending' | 'confirmed' | 'indexed' | 'error';

export interface TxStepMeta {
  hash?: Hex;
  approvalHash?: Hex;
  error?: TxError;
  receipt?: TransactionReceipt;
  /** Human label for the current step (already localized copy). */
  label: string;
  explorerUrl?: string;
}

export interface TxHooks {
  onStep?(step: TxStep, meta: TxStepMeta): void;
}

/** A fully-shaped contract write: what viem's writeContract needs. */
export interface TxRequest {
  address: Address;
  abi: Abi;
  functionName: string;
  args: readonly unknown[];
  value?: bigint;
}

export interface TxPlan {
  /** Shown in the modal title, e.g. "Buy Punk #4021". */
  title: string;
  /** Optional approval step executed before the main write. Return null to skip. */
  approval?: (ctx: WalletCtx) => Promise<TxRequest | null>;
  /** The main write. */
  request: (ctx: WalletCtx) => Promise<TxRequest>;
  /** Summary rows for the modal (label, value) — rendered BEFORE the step rail. */
  summary?: Array<[string, string]>;
  /** Success card: what changed + the one primary next action (spec B3). */
  success?: { message: string; action?: { label: string; href: string } };
}

export interface TxResult {
  hash: Hex;
  receipt: TransactionReceipt;
  /** true when the backend confirmed it indexed this tx (instant lane). */
  indexed: boolean;
}

export interface RunOptions {
  /** Tell the backend about the hash so it indexes immediately (default true). */
  observe?: boolean;
  /** How long to wait for the backend's `tx:<hash>` event before giving up (not an error). */
  indexTimeoutMs?: number;
}

const STEP_LABEL: Record<TxStep, string> = {
  idle: '',
  approve: 'Allow MagicWebb to move this NFT (one time)',
  sign: 'Confirm in your wallet',
  pending: 'Waiting for the network (~3s)',
  confirmed: 'Done',
  indexed: 'Done — the marketplace is up to date',
  error: 'Something went wrong',
};

function stepLabel(s: TxStep): string {
  return s === 'pending' ? `Waiting for ${currentChain().name} (~3s)` : STEP_LABEL[s];
}

/** Best effort: estimateGas × gasPrice → "≈ 0.02 C2FLR". Never throws. */
export async function estimateFee(ctx: WalletCtx, req: TxRequest): Promise<string | undefined> {
  try {
    const [gas, price] = await Promise.all([
      ctx.pub.estimateContractGas({
        account: ctx.account,
        address: req.address,
        abi: req.abi,
        functionName: req.functionName,
        args: req.args as unknown[],
        value: req.value,
      } as Parameters<typeof ctx.pub.estimateContractGas>[0]),
      ctx.pub.getGasPrice(),
    ]);
    const wei = BigInt(gas) * BigInt(price);
    if (wei <= 0n) return undefined;
    const text = fmtPrice(wei, 4);
    return `≈ ${text === '0' ? '<0.0001' : text} ${currentChain().currency}`;
  } catch {
    return undefined;
  }
}

async function send(ctx: WalletCtx, req: TxRequest): Promise<Hex> {
  // simulate first so reverts surface as decoded custom errors, not a wallet
  // popup that fails with "execution reverted".
  const { request } = await ctx.pub.simulateContract({
    account: ctx.account,
    address: req.address,
    abi: req.abi,
    functionName: req.functionName,
    args: req.args as unknown[],
    value: req.value,
  } as Parameters<typeof ctx.pub.simulateContract>[0]);
  return ctx.wallet.writeContract(request as Parameters<typeof ctx.wallet.writeContract>[0]);
}

/** Best-effort: ask the backend to index this tx right now (instant lane). */
async function observe(hash: Hex): Promise<void> {
  try {
    await fetch('/api/v1/tx/observe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ hash }),
      keepalive: true,
    });
  } catch { /* indexer will catch up on its own */ }
}

export async function runTx(plan: TxPlan, hooks: TxHooks = {}, opts: RunOptions = {}): Promise<TxResult> {
  const step = (s: TxStep, meta: Partial<TxStepMeta> = {}) => hooks.onStep?.(s, { label: stepLabel(s), ...meta });
  const chain = currentChain();
  // The plan is the source of the modal's "what happens next" rows and the
  // success card; mw.ts flow() only carries the title/approval flag.
  if (plan.summary) txModal.summary = plan.summary;
  txModal.feeEstimate = undefined;
  txModal.success = undefined;
  try {
    const ctx = await requireWallet();

    let approvalHash: Hex | undefined;
    if (plan.approval) {
      const ap = await plan.approval(ctx);
      if (ap) {
        step('approve');
        approvalHash = await send(ctx, ap);
        step('approve', { approvalHash, explorerUrl: explorerTx(approvalHash) });
        await ctx.pub.waitForTransactionReceipt({ hash: approvalHash, confirmations: chain.confirmations });
      }
    }

    step('sign', { approvalHash });
    const req = await plan.request(ctx);
    // Fee preflight is advisory: it must never delay or block the wallet prompt.
    void estimateFee(ctx, req).then((fee) => { if (fee) txModal.feeEstimate = fee; });
    const hash = await send(ctx, req);
    step('pending', { hash, approvalHash, explorerUrl: explorerTx(hash) });

    // Subscribe before the receipt lands so the indexed event cannot race us.
    const indexedP = opts.observe === false
      ? Promise.resolve(false)
      : ws.waitFor('tx-indexed', (p) => (p as { hash?: string })?.hash?.toLowerCase() === hash.toLowerCase(), opts.indexTimeoutMs ?? 8000, txChannel(hash)).then(() => true, () => false);

    const receipt = await ctx.pub.waitForTransactionReceipt({ hash, confirmations: chain.confirmations });
    if (receipt.status !== 'success') {
      throw new TxError('ContractRevert', 'The transaction was mined but reverted.', { hash });
    }
    if (opts.observe !== false) void observe(hash);
    const success = plan.success ?? { message: `${plan.title} — confirmed` };
    txModal.success = success;
    step('confirmed', { hash, approvalHash, receipt, explorerUrl: explorerTx(hash) });
    toastSuccess(success.message, success.action ? { action: { label: success.action.label, href: success.action.href } } : {});

    const indexed = await indexedP;
    if (indexed) step('indexed', { hash, approvalHash, receipt, explorerUrl: explorerTx(hash) });
    return { hash, receipt, indexed };
  } catch (e) {
    const err = decodeRevert(e);
    step('error', { error: err, hash: err.hash, explorerUrl: err.hash ? explorerTx(err.hash) : undefined });
    // A cancelled wallet prompt is not an error worth a toast — the modal says so.
    if (err.kind !== 'UserRejected') toastError(err.message);
    throw err;
  }
}
