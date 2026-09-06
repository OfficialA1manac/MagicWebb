// Global transaction-modal state (Svelte 5 runes, module-level).
// Pages call `runWithModal(plan-runner)`; TxModal.svelte renders `txModal`.
import type { TxStep, TxStepMeta, TxHooks, TxResult } from '../tx/runner';
import { TxError } from '../tx/errors';

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
  /** v3.6 Review step: the plan is shown and nothing is signed until confirm() runs. */
  reviewing: boolean;
  confirm?: () => void;
  cancelReview?: () => void;
}

const initial: TxModalState = { open: false, title: '', summary: [], step: 'idle', hasApproval: false, reviewing: false };

export const txModal = $state<TxModalState>({ ...initial });

export function closeTxModal(): void {
  if (txModal.step === 'sign' || txModal.step === 'pending' || txModal.step === 'approve') return; // cannot abandon a live wallet prompt
  if (txModal.reviewing && txModal.cancelReview) { txModal.cancelReview(); return; }
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
    step: 'idle',
    reviewing: true,
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
  // A retry skips Review (the user already confirmed this exact plan).
  txModal.retry = () => { txModal.reviewing = false; txModal.step = 'sign'; void run(hooks).catch(() => undefined); };
  // Review step (v3.6): show the plan; sign only after Confirm. Cancel here
  // rejects like a wallet rejection so callers' error paths stay identical.
  return new Promise<T>((resolve, reject) => {
    txModal.confirm = () => {
      txModal.reviewing = false;
      txModal.step = 'sign';
      run(hooks).then(resolve, reject);
    };
    txModal.cancelReview = () => {
      Object.assign(txModal, { ...initial });
      reject(new TxError('UserRejected', 'Cancelled before signing — nothing was sent.'));
    };
  });
}
