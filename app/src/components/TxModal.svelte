<script lang="ts">
  // Approved wireframe (design review 2026-08-20, D8): bottom sheet < 480px,
  // centered dialog otherwise. Spec B3 order: title → "what happens next"
  // summary <dl> → estimated network fee → step rail → success card with one
  // primary next action. Plain-language errors with the one fix each needs.
  import { onMount } from 'svelte';
  import { fade, fly } from 'svelte/transition';
  import { txModal, closeTxModal } from '../lib/stores/txmodal.svelte';
  import { shortAddr, copyText } from '../lib/format';
  import { currentChain, faucetUrl } from '../lib/chains';
  import { switchToSiteChain, waitForWagmi } from '../lib/tx/client';
  import { toastError, toastSuccess } from '../lib/toast.svelte';
  import Hint from './Hint.svelte';

  let dialog: HTMLDivElement | undefined = $state();
  let chainName = $state('the network');
  let chainId = $state(0);
  onMount(() => { const c = currentChain(); chainName = c.name; chainId = c.id; });

  type Rail = { key: string; label: string; state: 'done' | 'active' | 'todo' | 'error' };
  const order = ['approve', 'sign', 'pending', 'confirmed'] as const;

  let rail = $derived.by((): Rail[] => {
    const s = txModal.step;
    const idx = s === 'indexed' ? 4 : s === 'error' ? -1 : order.indexOf(s as typeof order[number]);
    const steps: Rail[] = [];
    const labels: Record<string, string> = {
      approve: 'Allow MagicWebb to move this NFT (one time)',
      sign: 'Confirm in your wallet',
      pending: `Waiting for ${chainName} (~3s)`,
      confirmed: 'Done',
    };
    order.forEach((k, i) => {
      if (k === 'approve' && !txModal.hasApproval) return;
      let st: Rail['state'] = 'todo';
      if (s === 'error') st = i < Math.max(0, lastGoodIndex()) ? 'done' : i === Math.max(0, lastGoodIndex()) ? 'error' : 'todo';
      else if (i < idx) st = 'done';
      else if (i === idx) st = s === 'confirmed' ? 'done' : 'active';
      if (s === 'indexed') st = 'done';
      steps.push({ key: k, label: labels[k], state: st });
    });
    return steps;
  });

  // Where the error happened: if we have a hash it failed after pending, else at sign.
  function lastGoodIndex() { return txModal.hash ? 2 : txModal.approvalHash ? 1 : (txModal.hasApproval ? 0 : 1); }

  let busy = $derived(txModal.step === 'approve' || txModal.step === 'sign' || txModal.step === 'pending');
  // "Step X of N": Review counts as step 1; the rail entries follow.
  let stepTotal = $derived(1 + rail.length);
  let stepNow = $derived(txModal.reviewing ? 1 : 1 + Math.max(1, rail.findIndex((r) => r.state === 'active' || r.state === 'error') + 1));
  const APPROVE_TIP = 'A one-time permission so the marketplace contract can move this NFT when it sells. It stays in your wallet until then. You can revoke it any time in your wallet.';
  let done = $derived(txModal.step === 'confirmed' || txModal.step === 'indexed');
  // One primary next action: the plan's own success card wins over the page's fallback.
  let cta = $derived(txModal.success?.action ?? txModal.successAction);
  let successMessage = $derived(txModal.success?.message ?? (txModal.step === 'indexed' ? 'Confirmed and live on the marketplace.' : `Confirmed on ${chainName}.`));
  let faucet = $derived(chainId === 114 ? faucetUrl() : null);

  let switching = $state(false);
  async function doSwitch() {
    switching = true;
    try {
      await switchToSiteChain(await waitForWagmi());
      txModal.retry?.();
    } catch {
      toastError(`Could not switch automatically. Open your wallet and choose ${chainName}.`);
    } finally { switching = false; }
  }

  async function copyDetails() {
    const e = txModal.error;
    const lines = [
      `MagicWebb · ${txModal.title}`,
      `kind: ${e?.kind ?? 'unknown'}${e?.revertName ? ` (${e.revertName})` : ''}`,
      `message: ${e?.message ?? ''}`,
      txModal.hash ? `tx: ${txModal.hash}` : '',
      `chain: ${chainName} (${chainId})`,
      `cause: ${String((e?.cause as { message?: string } | undefined)?.message ?? e?.cause ?? '')}`.slice(0, 500),
    ].filter(Boolean);
    if (await copyText(lines.join('\n'))) toastSuccess('Details copied');
    else toastError('Could not copy — select the text instead.');
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && !busy) closeTxModal();
    if (e.key === 'Tab' && dialog) {
      const f = dialog.querySelectorAll<HTMLElement>('button, a[href], [tabindex]:not([tabindex="-1"])');
      // Busy steps render no focusable element: keep focus on the dialog
      // itself so Tab cannot escape to the page behind the scrim.
      if (!f.length) { dialog.focus(); e.preventDefault(); return; }
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
  }

  let lastFocused: HTMLElement | null = null;
  $effect(() => {
    if (txModal.open && dialog) {
      lastFocused ??= document.activeElement as HTMLElement | null;
      const btn = dialog.querySelector<HTMLElement>('button');
      const target = dialog;
      queueMicrotask(() => (btn ?? target)?.focus());
      document.body.style.overflow = 'hidden';
    } else {
      document.body.style.overflow = '';
      lastFocused?.focus();
      lastFocused = null;
    }
  });
</script>

<svelte:window onkeydown={txModal.open ? onKey : undefined} />

{#if txModal.open}
  <div class="mw-tx-scrim" role="presentation" transition:fade={{ duration: 150 }} onclick={() => { if (!busy) closeTxModal(); }}></div>
  <div class="mw-tx-sheet" role="dialog" aria-modal="true" aria-labelledby="mw-tx-title" tabindex="-1" bind:this={dialog} transition:fly={{ y: 24, duration: 220 }}>
    <div class="mw-tx-grab" aria-hidden="true"></div>
    <h2 id="mw-tx-title">{txModal.title}</h2>

    {#if txModal.step === 'error'}
      <div class="mw-tx-error" role="alert">
        <div class="mw-tx-error-title">
          {#if txModal.error?.kind === 'UserRejected'}Cancelled
          {:else if txModal.error?.kind === 'WalletRequired'}Wallet needed
          {:else if txModal.error?.kind === 'WrongChain'}Wrong network
          {:else if txModal.error?.kind === 'InsufficientFunds'}Not enough funds
          {:else}Could not complete{/if}
        </div>
        <p>{txModal.error?.message ?? 'Something went wrong.'}</p>
        {#if txModal.explorerUrl}<a class="mw-tx-link" href={txModal.explorerUrl} target="_blank" rel="noopener">View on explorer ↗</a>{/if}
      </div>
      <div class="mw-tx-actions">
        {#if txModal.error?.kind === 'WrongChain'}
          <button class="mw-btn mw-btn-primary" onclick={doSwitch} disabled={switching}>{switching ? 'Switching…' : `Switch to ${chainName}`}</button>
        {:else if txModal.error?.kind === 'InsufficientFunds' && faucet}
          <a class="mw-btn mw-btn-primary" href={faucet} target="_blank" rel="noopener">Get test FLR ↗</a>
          <button class="mw-btn mw-btn-ghost" onclick={() => txModal.retry?.()}>Try again</button>
        {:else if txModal.error?.kind === 'UserRejected' || txModal.error?.kind === 'WalletRequired' || txModal.error?.kind === 'InsufficientFunds'}
          <button class="mw-btn mw-btn-primary" onclick={() => txModal.retry?.()}>Try again</button>
        {:else}
          <button class="mw-btn mw-btn-primary" onclick={() => txModal.retry?.()}>Try again</button>
          <button class="mw-btn mw-btn-ghost" onclick={copyDetails}>Copy details</button>
        {/if}
        <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Close</button>
      </div>
    {:else}
      {#if txModal.summary.length}
        <dl class="mw-tx-summary" aria-label="What happens next">
          {#each txModal.summary as [k, v], i (i)}
            <div class="mw-tx-row" class:is-total={i === 0}>
              <dt>{k}</dt><dd class:mono={/\d/.test(v)}>{v}</dd>
            </div>
          {/each}
          {#if txModal.feeEstimate && !done}
            <div class="mw-tx-row mw-tx-fee">
              <dt>Network fee</dt><dd class="mono">{txModal.feeEstimate}</dd>
            </div>
          {/if}
        </dl>
      {:else if txModal.feeEstimate && !done}
        <dl class="mw-tx-summary" aria-label="What happens next">
          <div class="mw-tx-row mw-tx-fee"><dt>Network fee</dt><dd class="mono">{txModal.feeEstimate}</dd></div>
        </dl>
      {/if}

      <p class="mw-tx-position mw-tx-muted" aria-live="polite">Step {done ? stepTotal : stepNow} of {stepTotal}{#if txModal.reviewing} · Review{/if}</p>
      <ol class="mw-tx-rail" aria-live="polite">
        <li class="mw-tx-step is-{txModal.reviewing ? 'active' : 'done'}">
          <span class="mw-tx-dot" aria-hidden="true">{#if !txModal.reviewing}✓{/if}</span>
          <span class="mw-tx-step-label">Review what will happen</span>
        </li>
        {#each rail as r (r.key)}
          <li class="mw-tx-step is-{r.state}">
            <span class="mw-tx-dot" aria-hidden="true">{#if r.state === 'done'}✓{/if}</span>
            <span class="mw-tx-step-label">
              {r.label}
              {#if r.key === 'approve'}<Hint text={APPROVE_TIP} label="Why do I need to allow this?" />{/if}
              {#if r.key === 'pending' && txModal.hash}
                · <a class="mw-tx-link mono" href={txModal.explorerUrl} target="_blank" rel="noopener">{shortAddr(txModal.hash, 6, 4)}</a>
              {/if}
              {#if r.key === 'approve' && txModal.approvalHash}
                · <span class="mono mw-tx-muted">{shortAddr(txModal.approvalHash, 6, 4)}</span>
              {/if}
            </span>
          </li>
        {/each}
      </ol>

      {#if done}
        <div class="mw-tx-done" role="status">
          <div class="mw-tx-check" aria-hidden="true">✓</div>
          <div>
            <p class="mw-tx-done-title">{successMessage}</p>
            <p class="mw-tx-muted mw-tx-done-sub">{txModal.step === 'indexed' ? 'Live on the marketplace.' : 'The page is updating.'}</p>
          </div>
        </div>
      {/if}

      <div class="mw-tx-actions">
        {#if done}
          {#if cta}<a class="mw-btn mw-btn-primary" href={cta.href}>{cta.label}</a>{/if}
          <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Done</button>
        {:else if txModal.reviewing}
          <button class="mw-btn mw-btn-primary" onclick={() => txModal.confirm?.()}>Confirm</button>
          <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Cancel</button>
        {:else if busy}
          <p class="mw-tx-muted mw-tx-hint">{txModal.step === 'pending' ? 'You can keep this open or come back later — nothing else to sign.' : 'Open your wallet to continue. This can\'t be cancelled here — decline in your wallet instead.'}</p>
        {:else}
          <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Cancel</button>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .mw-tx-scrim { position: fixed; inset: 0; background: rgba(0,0,0,.6); z-index: 200; backdrop-filter: blur(2px); }
  .mw-tx-sheet {
    position: fixed; left: 0; right: 0; bottom: 0; z-index: 201;
    background: var(--ink-900, var(--surface)); color: var(--white, var(--text));
    border-top: 1px solid var(--white-10); border-radius: 24px 24px 0 0;
    padding: 12px 20px calc(24px + env(safe-area-inset-bottom)); box-shadow: 0 -20px 60px rgba(0,0,0,.6);
    max-height: 92vh; overflow-y: auto; font-family: inherit;
  }
  @media (min-width: 480px) {
    .mw-tx-sheet { left: 50%; right: auto; bottom: auto; top: 50%; transform: translate(-50%, -50%); width: min(440px, calc(100vw - 32px)); border-radius: 20px; border: 1px solid var(--white-10); padding: 20px 24px 24px; }
    .mw-tx-grab { display: none; }
  }
  .mw-tx-grab { width: 40px; height: 4px; border-radius: 2px; background: var(--white-20); margin: 0 auto 14px; }
  h2 { font-size: 17px; font-weight: 700; margin: 0 0 4px; }
  .mw-tx-rail { list-style: none; margin: 14px 0 6px; padding: 0; }
  .mw-tx-step { display: flex; gap: 12px; align-items: center; min-height: 44px; font-size: 14px; color: var(--white-60); }
  .mw-tx-dot { width: 24px; height: 24px; border-radius: 50%; border: 2px solid var(--white-20); display: inline-flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; }
  .is-done { color: var(--text); } .is-done .mw-tx-dot { background: #4ade80; border-color: #4ade80; color: var(--bg); font-weight: 800; }
  .is-active { color: var(--text); font-weight: 600; } .is-active .mw-tx-dot { border-color: #7dd3fc; border-top-color: transparent; animation: mw-spin 1s linear infinite; }
  .is-error .mw-tx-dot { border-color: #f87171; }
  @keyframes mw-spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .is-active .mw-tx-dot { animation: none; border-top-color: #7dd3fc; } }
  .mw-tx-summary { margin: 10px 0 0; }
  .mw-tx-row { display: flex; justify-content: space-between; gap: 12px; font-size: 14px; padding: 9px 0; border-bottom: 1px solid var(--white-10); }
  .mw-tx-row dt { color: var(--white-60); } .mw-tx-row dd { margin: 0; text-align: right; }
  .mw-tx-row.is-total { font-weight: 700; font-size: 15px; } .mw-tx-row.is-total dt { color: var(--text); }
  .mw-tx-fee { font-size: 13px; } .mw-tx-fee dd { color: var(--white-80); }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
  .mw-tx-muted { color: var(--white-60); }
  .mw-tx-hint { font-size: 13px; text-align: center; margin: 6px 0 0; }
  .mw-tx-link { color: #7dd3fc; text-decoration: underline; }
  .mw-tx-done { display: flex; align-items: center; gap: 12px; padding: 8px 0 4px; font-size: 14px; }
  .mw-tx-done p { margin: 0; }
  .mw-tx-done-title { font-weight: 700; font-size: 15px; }
  .mw-tx-done-sub { font-size: 13px; }
  .mw-tx-check { width: 40px; height: 40px; border-radius: 50%; background: #4ade80; color: var(--bg); font-weight: 800; font-size: 20px; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .mw-tx-error { background: rgba(248,113,113,.08); border: 1px solid rgba(248,113,113,.3); border-radius: 14px; padding: 14px; margin: 12px 0; font-size: 14px; }
  .mw-tx-error-title { font-weight: 700; color: #fca5a5; margin-bottom: 6px; }
  .mw-tx-error p { margin: 0 0 8px; color: var(--white-80); line-height: 1.5; }
  .mw-tx-actions { display: flex; flex-direction: column; gap: 8px; margin-top: 14px; }
  .mw-btn { min-height: 44px; border-radius: 12px; font-weight: 700; font-size: 15px; display: flex; align-items: center; justify-content: center; border: 1px solid transparent; cursor: pointer; font-family: inherit; text-decoration: none; }
  .mw-btn:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
  .mw-btn[disabled] { opacity: .6; cursor: default; }
  .mw-btn-primary { background: linear-gradient(135deg, #7dd3fc, #0ea5e9); color: var(--bg); }
  .mw-btn-ghost { background: transparent; color: var(--text); border-color: var(--white-10); }
</style>
