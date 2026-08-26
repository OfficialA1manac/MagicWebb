<script lang="ts">
  // Approved wireframe (design review 2026-08-20, D8): bottom sheet < 480px,
  // centered dialog otherwise; 4-step rail; cost summary; plain-language errors.
  import { onMount } from 'svelte';
  import { fade, fly } from 'svelte/transition';
  import { txModal, closeTxModal } from '../lib/stores/txmodal.svelte';
  import { shortAddr } from '../lib/format';
  import { currentChain } from '../lib/chains';

  let dialog: HTMLDivElement | undefined = $state();
  let chainName = $state('the network');
  onMount(() => { chainName = currentChain().name; });

  type Rail = { key: string; label: string; state: 'done' | 'active' | 'todo' | 'error' };
  const order = ['approve', 'sign', 'pending', 'confirmed'] as const;

  let rail = $derived.by((): Rail[] => {
    const s = txModal.step;
    const idx = s === 'indexed' ? 4 : s === 'error' ? -1 : order.indexOf(s as typeof order[number]);
    const steps: Rail[] = [];
    const labels: Record<string, string> = {
      approve: 'Approve the marketplace for this collection',
      sign: 'Confirm in your wallet',
      pending: `Waiting for ${chainName}`,
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
  let done = $derived(txModal.step === 'confirmed' || txModal.step === 'indexed');

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && !busy) closeTxModal();
    if (e.key === 'Tab' && dialog) {
      const f = dialog.querySelectorAll<HTMLElement>('button, a[href], [tabindex]:not([tabindex="-1"])');
      if (!f.length) return;
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
  }

  $effect(() => {
    if (txModal.open && dialog) {
      const btn = dialog.querySelector<HTMLElement>('button');
      queueMicrotask(() => btn?.focus());
      document.body.style.overflow = 'hidden';
    } else {
      document.body.style.overflow = '';
    }
  });
</script>

<svelte:window onkeydown={txModal.open ? onKey : undefined} />

{#if txModal.open}
  <div class="mw-tx-scrim" role="presentation" transition:fade={{ duration: 150 }} onclick={() => { if (!busy) closeTxModal(); }}></div>
  <div class="mw-tx-sheet" role="dialog" aria-modal="true" aria-labelledby="mw-tx-title" bind:this={dialog} transition:fly={{ y: 24, duration: 220 }}>
    <div class="mw-tx-grab" aria-hidden="true"></div>
    <h2 id="mw-tx-title">{txModal.title}</h2>

    {#if txModal.step === 'error'}
      <div class="mw-tx-error" role="alert">
        <div class="mw-tx-error-title">
          {#if txModal.error?.kind === 'UserRejected'}Transaction cancelled
          {:else if txModal.error?.kind === 'WalletRequired'}Wallet needed
          {:else if txModal.error?.kind === 'WrongChain'}Wrong network
          {:else if txModal.error?.kind === 'InsufficientFunds'}Not enough funds
          {:else if txModal.error?.kind === 'Paused'}Marketplace paused
          {:else}Could not complete{/if}
        </div>
        <p>{txModal.error?.message ?? 'Something went wrong.'}</p>
        {#if txModal.explorerUrl}<a class="mw-tx-link" href={txModal.explorerUrl} target="_blank" rel="noopener">View on explorer ↗</a>{/if}
      </div>
      <div class="mw-tx-actions">
        {#if txModal.error?.kind !== 'Paused'}<button class="mw-btn mw-btn-primary" onclick={() => txModal.retry?.()}>Try again</button>{/if}
        <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Close</button>
      </div>
    {:else}
      <ol class="mw-tx-rail" aria-live="polite">
        {#each rail as r (r.key)}
          <li class="mw-tx-step is-{r.state}">
            <span class="mw-tx-dot" aria-hidden="true">{#if r.state === 'done'}✓{/if}</span>
            <span class="mw-tx-step-label">
              {r.label}
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
        <div class="mw-tx-done">
          <div class="mw-tx-check" aria-hidden="true">✓</div>
          <p>{txModal.step === 'indexed' ? 'Confirmed and live on the marketplace.' : `Confirmed on ${chainName}. The page is updating.`}</p>
        </div>
      {/if}

      {#if txModal.summary.length}
        <dl class="mw-tx-summary">
          {#each txModal.summary as [k, v], i}
            <div class="mw-tx-row" class:is-total={i === txModal.summary.length - 1}>
              <dt>{k}</dt><dd class="mono">{v}</dd>
            </div>
          {/each}
        </dl>
      {/if}

      <div class="mw-tx-actions">
        {#if done}
          {#if txModal.successAction}<a class="mw-btn mw-btn-primary" href={txModal.successAction.href}>{txModal.successAction.label}</a>{/if}
          <button class="mw-btn mw-btn-ghost" onclick={closeTxModal}>Done</button>
        {:else if busy}
          <p class="mw-tx-muted mw-tx-hint">{txModal.step === 'pending' ? 'You can keep this open or come back later — nothing else to sign.' : 'Open your wallet to continue.'}</p>
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
    background: var(--ink-900, #0f0f13); color: var(--white, #fafafa);
    border-top: 1px solid rgba(255,255,255,.1); border-radius: 24px 24px 0 0;
    padding: 12px 20px calc(24px + env(safe-area-inset-bottom)); box-shadow: 0 -20px 60px rgba(0,0,0,.6);
    max-height: 92vh; overflow-y: auto; font-family: inherit;
  }
  @media (min-width: 480px) {
    .mw-tx-sheet { left: 50%; right: auto; bottom: auto; top: 50%; transform: translate(-50%, -50%); width: min(440px, calc(100vw - 32px)); border-radius: 20px; border: 1px solid rgba(255,255,255,.1); padding: 20px 24px 24px; }
    .mw-tx-grab { display: none; }
  }
  .mw-tx-grab { width: 40px; height: 4px; border-radius: 2px; background: rgba(255,255,255,.15); margin: 0 auto 14px; }
  h2 { font-size: 17px; font-weight: 700; margin: 0 0 4px; }
  .mw-tx-rail { list-style: none; margin: 14px 0 6px; padding: 0; }
  .mw-tx-step { display: flex; gap: 12px; align-items: center; min-height: 44px; font-size: 14px; color: rgba(255,255,255,.55); }
  .mw-tx-dot { width: 24px; height: 24px; border-radius: 50%; border: 2px solid rgba(255,255,255,.15); display: inline-flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; }
  .is-done { color: #fafafa; } .is-done .mw-tx-dot { background: #4ade80; border-color: #4ade80; color: #09090b; font-weight: 800; }
  .is-active { color: #fafafa; font-weight: 600; } .is-active .mw-tx-dot { border-color: #7dd3fc; border-top-color: transparent; animation: mw-spin 1s linear infinite; }
  .is-error .mw-tx-dot { border-color: #f87171; }
  @keyframes mw-spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .is-active .mw-tx-dot { animation: none; border-top-color: #7dd3fc; } }
  .mw-tx-summary { margin: 10px 0 0; }
  .mw-tx-row { display: flex; justify-content: space-between; gap: 12px; font-size: 14px; padding: 9px 0; border-bottom: 1px solid rgba(255,255,255,.08); }
  .mw-tx-row dt { color: rgba(255,255,255,.6); } .mw-tx-row dd { margin: 0; text-align: right; }
  .mw-tx-row.is-total { border: 0; font-weight: 700; font-size: 15px; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
  .mw-tx-muted { color: rgba(255,255,255,.5); }
  .mw-tx-hint { font-size: 13px; text-align: center; margin: 6px 0 0; }
  .mw-tx-link { color: #7dd3fc; text-decoration: underline; }
  .mw-tx-done { display: flex; align-items: center; gap: 12px; padding: 8px 0 4px; font-size: 14px; }
  .mw-tx-check { width: 40px; height: 40px; border-radius: 50%; background: #4ade80; color: #09090b; font-weight: 800; font-size: 20px; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .mw-tx-error { background: rgba(248,113,113,.08); border: 1px solid rgba(248,113,113,.3); border-radius: 14px; padding: 14px; margin: 12px 0; font-size: 14px; }
  .mw-tx-error-title { font-weight: 700; color: #fca5a5; margin-bottom: 6px; }
  .mw-tx-error p { margin: 0 0 8px; color: rgba(255,255,255,.8); line-height: 1.5; }
  .mw-tx-actions { display: flex; flex-direction: column; gap: 8px; margin-top: 14px; }
  .mw-btn { min-height: 44px; border-radius: 12px; font-weight: 700; font-size: 15px; display: flex; align-items: center; justify-content: center; border: 1px solid transparent; cursor: pointer; font-family: inherit; text-decoration: none; }
  .mw-btn:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
  .mw-btn-primary { background: linear-gradient(135deg, #7dd3fc, #0ea5e9); color: #09090b; }
  .mw-btn-ghost { background: transparent; color: #fafafa; border-color: rgba(255,255,255,.14); }
</style>
