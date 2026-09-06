<script lang="ts">
  // First-run strip (v3.6 wave 5c). Two placements:
  //   hero  — the home hero's right column (HomeSections), always rendered
  //           while the strip is visible;
  //   page  — token/auction/collection pages, only for wallet-less visitors
  //           on trading networks (a connected user has moved past it).
  // State table + persisted keys live in lib/firstrun.ts.
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import { currentChain, tradingLive, isTestnet, faucetUrl } from '../lib/chains';
  import { firstRunSteps, stripState, STRIP_DISMISSED_KEY, FIRST_TRADE_KEY, FIRSTRUN_CHANGED_EVENT, openNoWalletSheet } from '../lib/firstrun';

  let { placement = 'page' }: { placement?: 'hero' | 'page' } = $props();

  const chain = currentChain();
  const trading = tradingLive();
  const testnet = isTestnet(chain.id);
  const faucet = faucetUrl();

  let connected = $state(false);
  let dismissed = $state(false);
  let traded = $state(false);
  let mounted = $state(false);

  let steps = $derived(firstRunSteps({ connected, testnet, faucetUrl: faucet, traded }));
  let view = $derived(stripState({ dismissed, traded, connected }));
  // Page placement is for people who have not connected yet; the hero keeps
  // showing progress (✓ steps) until the strip is dismissed.
  let show = $derived(mounted && trading && (placement === 'hero' ? view.visible : view.visible && !connected));
  let showRestore = $derived(mounted && trading && placement === 'page' && view.mode === 'hidden' && !connected);

  function readWallet() {
    try { connected = !!window.MW?.address?.() || !!(localStorage.getItem('mw_addr') || '').match(/^0x[0-9a-fA-F]{40}$/); } catch { connected = false; }
  }
  function readStore() {
    try {
      dismissed = localStorage.getItem(STRIP_DISMISSED_KEY) === '1';
      traded = localStorage.getItem(FIRST_TRADE_KEY) === '1';
    } catch { /* private mode */ }
  }
  function persistDismissed(v: boolean) {
    dismissed = v;
    try { if (v) localStorage.setItem(STRIP_DISMISSED_KEY, '1'); else localStorage.removeItem(STRIP_DISMISSED_KEY); } catch { /* private mode */ }
    window.dispatchEvent(new CustomEvent(FIRSTRUN_CHANGED_EVENT, { detail: { dismissed: v } }));
  }

  onMount(() => {
    readStore();
    readWallet();
    mounted = true;
    const onWallet = () => readWallet();
    const onChanged = () => readStore();
    window.addEventListener('mw-wallet-changed', onWallet);
    window.addEventListener('mw-ready', onWallet);
    window.addEventListener(FIRSTRUN_CHANGED_EVENT, onChanged);
    window.addEventListener('storage', onChanged);
    return () => {
      window.removeEventListener('mw-wallet-changed', onWallet);
      window.removeEventListener('mw-ready', onWallet);
      window.removeEventListener(FIRSTRUN_CHANGED_EVENT, onChanged);
      window.removeEventListener('storage', onChanged);
    };
  });
</script>

{#if show}
  <section class="hs-strip fr fr-{placement} fr-mode-{view.mode}" aria-label="Getting started" data-testid="first-run-strip">
    {#if view.mode === 'done'}
      <span class="hs-step is-done fr-set"><span class="hs-step-n" aria-hidden="true"><Icon name="check" size={14} /></span>You're set — you've connected, funded and traded.</span>
    {:else}
      {#each steps as s (s.n)}
        <span class="hs-step" class:is-done={s.done} class:is-current={view.current === s.n} aria-current={view.current === s.n ? 'step' : undefined}>
          <span class="hs-step-n" aria-hidden="true">{#if s.done}<Icon name="check" size={14} />{:else}{s.n}{/if}</span>
          {#if s.href}
            <a href={s.href} target="_blank" rel="noopener">{s.label} <Icon name="external" size={14} /></a>
          {:else}
            <span class="fr-label">{s.label}</span>
          {/if}
          {#if s.noWallet}
            <button type="button" class="fr-nowallet" onclick={openNoWalletSheet}>No wallet?</button>
          {/if}
        </span>
      {/each}
    {/if}
    {#if view.dismissible}
      <button type="button" class="hs-strip-x" aria-label="Dismiss getting started" onclick={() => persistDismissed(true)}><Icon name="x" size={16} /></button>
    {/if}
  </section>
{:else if showRestore}
  <p class="fr-restore"><button type="button" class="fr-restore-btn" onclick={() => persistDismissed(false)}>Show getting-started steps</button></p>
{/if}

<style>
  .hs-strip { max-width: 72rem; margin: 0 auto var(--sp-6); padding: var(--sp-3) var(--sp-4); display: flex; align-items: center; gap: var(--sp-4); flex-wrap: wrap; border: 1px solid var(--line); border-radius: var(--r-card); background: var(--surface); position: relative; }
  .fr-hero { flex-direction: column; align-items: flex-start; gap: var(--sp-3); margin: 0; }
  .fr-page { margin: 0 0 var(--sp-4); }
  .hs-step { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--fs-small); color: var(--text-2); font-weight: 600; min-height: var(--hit); }
  .hs-step.is-done { color: var(--green); }
  .hs-step.is-current { color: var(--text); }
  .hs-step.is-current .hs-step-n { border-color: var(--sky); color: var(--sky); background: var(--sky-12); }
  .hs-step a { color: var(--link-strong); display: inline-flex; align-items: center; gap: 4px; min-height: var(--hit); }
  .hs-step-n { display: inline-flex; align-items: center; justify-content: center; width: 22px; height: 22px; border-radius: var(--r-pill); border: 1px solid var(--line-strong); font-size: var(--fs-caption); font-weight: 700; flex: 0 0 auto; }
  .hs-step.is-done .hs-step-n { border-color: var(--green); color: var(--green); }
  .fr-nowallet { min-height: var(--hit); padding: 0 var(--sp-2); border: 0; background: transparent; color: var(--link-strong); font: inherit; font-size: var(--fs-small); font-weight: 700; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; border-radius: var(--r-control); }
  .hs-strip-x { margin-left: auto; display: inline-flex; align-items: center; justify-content: center; width: var(--hit); height: var(--hit); border: 0; border-radius: var(--r-pill); background: transparent; color: var(--text-3); cursor: pointer; }
  .hs-strip-x:hover { color: var(--text); }
  .fr-restore { margin: 0 0 var(--sp-3); }
  .fr-restore-btn { min-height: var(--hit); padding: 0 var(--sp-2); border: 0; background: transparent; color: var(--text-3); font: inherit; font-size: var(--fs-small); font-weight: 600; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; border-radius: var(--r-control); }
  .fr-restore-btn:hover { color: var(--text); }
  @media (max-width: 640px) { .hs-strip { flex-direction: column; align-items: flex-start; gap: var(--sp-2); } .hs-strip-x { position: absolute; top: var(--sp-1); right: var(--sp-1); margin: 0; } }
</style>
