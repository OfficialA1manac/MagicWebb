// Global transaction-modal state (Svelte 5 runes, module-level).
// Pages call `runWithModal(plan-runner)`; TxModal.svelte renders `txModal`.
import type { TxStep, TxStepMeta, TxHooks, TxResult } from '../tx/runner';
import type { TxError } from '../tx/errors';

export interface TxModalState {
  open: boolean;
  title: string;
  summary: Array<[string, string]>;
  step: TxStep;
  hash?: string;
  approvalHash?: string;
  explorerUrl?: string;
  error?: TxError;
  hasApproval: boolean;
  /** Called when the user presses "Try again" in the error state. */
  retry?: () => void;
  /** Optional CTA shown on success (e.g. "View in my profile"). */
  successAction?: { label: string; href: string };
  /** Estimated network fee, e.g. "≈ 0.02 C2FLR" (best effort; unset on failure). */
  feeEstimate?: string;
  /** Success card copy set by the runner from `plan.success` (what changed + next action). */
  success?: { message: string; action?: { label: string; href: string } };
}

const initial: TxModalState = { open: false, title: '', summary: [], step: 'idle', hasApproval: false };

export const txModal = $state<TxModalState>({ ...initial });

export function closeTxModal(): void {
  if (txModal.step === 'sign' || txModal.step === 'pending' || txModal.step === 'approve') return; // cannot abandon a live wallet prompt
  Object.assign(txModal, { ...initial });
}

/** Force-close (used when a page navigates away). */
export function resetTxModal(): void { Object.assign(txModal, { ...initial }); }

/**
 * Run a tx flow with the modal wired up. `run` receives hooks to pass into
 * the tx function. Resolves with the TxResult or rejects with TxError.
 */
export async function runWithModal<T extends TxResult>(
  opts: { title: string; summary?: Array<[string, string]>; hasApproval?: boolean; successAction?: TxModalState['successAction'] },
  run: (hooks: TxHooks) => Promise<T>,
): Promise<T> {
  Object.assign(txModal, {
    ...initial,
    open: true,
    title: opts.title,
    summary: opts.summary ?? [],
    hasApproval: !!opts.hasApproval,
    step: 'sign',
    successAction: opts.successAction,
  });
  const hooks: TxHooks = {
    onStep(step: TxStep, meta: TxStepMeta) {
      txModal.step = step;
      if (meta.hash) txModal.hash = meta.hash;
      if (meta.approvalHash) { txModal.approvalHash = meta.approvalHash; txModal.hasApproval = true; }
      if (meta.explorerUrl) txModal.explorerUrl = meta.explorerUrl;
      if (meta.error) txModal.error = meta.error;
    },
  };
  // Swallow the retry rejection: the tx hooks already surface the error in
  // the modal, so a failed retry must not become an unhandled rejection.
  txModal.retry = () => { void runWithModal(opts, run).catch(() => undefined); };
  return run(hooks);
}
