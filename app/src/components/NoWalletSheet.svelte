<script lang="ts">
  // "I don't have a wallet" (v3.6 wave 5c). Mounted once in MwRuntime;
  // opened by the NOWALLET_OPEN_EVENT window event from the Connect control,
  // the mobile drawer and the first-run strip. Three lines, two install
  // links, one way back: "I have one now → Connect". Focus returns to the
  // control that opened it.
  import { onMount } from 'svelte';
  import { fade, scale } from 'svelte/transition';
  import Icon from './Icon.svelte';
  import { NOWALLET_OPEN_EVENT, WALLET_LINKS } from '../lib/firstrun';
  import { currentChain } from '../lib/chains';

  let open = $state(false);
  let dialog = $state<HTMLDivElement | undefined>();
  let lastFocused: HTMLElement | null = null;
  // Popup blocked → inline fallback link for that wallet.
  let blocked = $state<string | null>(null);
  let opened = $state<string | null>(null);
  const chain = currentChain();

  function show() {
    lastFocused = document.activeElement as HTMLElement | null;
    blocked = null; opened = null;
    open = true;
  }
  function close() {
    open = false;
    queueMicrotask(() => lastFocused?.focus());
  }
  function connectNow() {
    close();
    window.__MW_APPKIT_OPEN__?.();
  }
  function install(e: MouseEvent, name: string, href: string) {
    e.preventDefault();
    const w = window.open(href, '_blank', 'noopener');
    if (!w) { blocked = name; return; }
    opened = name;
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); close(); return; }
    if (e.key === 'Tab' && dialog) {
      const f = Array.from(dialog.querySelectorAll<HTMLElement>('button, a[href]'));
      if (!f.length) return;
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
  }

  $effect(() => {
    if (open && dialog) {
      const first = dialog.querySelector<HTMLElement>('button, a[href]');
      queueMicrotask(() => (first ?? dialog)?.focus());
      document.body.style.overflow = 'hidden';
      return () => { document.body.style.overflow = ''; };
    }
  });

  onMount(() => {
    window.addEventListener(NOWALLET_OPEN_EVENT, show);
    return () => window.removeEventListener(NOWALLET_OPEN_EVENT, show);
  });
</script>

<svelte:window onkeydown={open ? onKey : undefined} />

{#if open}
  <div class="nw-scrim" role="presentation" transition:fade={{ duration: 120 }} onclick={close}></div>
  <div class="nw-sheet" role="dialog" aria-modal="true" aria-labelledby="nw-title" aria-describedby="nw-body" tabindex="-1" bind:this={dialog} transition:scale={{ start: 0.98, duration: 120 }} data-testid="nowallet-sheet">
    <div class="nw-head">
      <h2 id="nw-title">Don't have a wallet yet?</h2>
      <button type="button" class="icon-btn" aria-label="Close" onclick={close}><Icon name="x" size={20} /></button>
    </div>
    <ol id="nw-body" class="nw-lines">
      <li><strong>A wallet is a free app</strong> that holds your NFTs and {chain.currency}. It is your login here — no email, no password.</li>
      <li><strong>Install one</strong> (takes about two minutes), then create an account inside it and write down the recovery phrase it gives you.</li>
      <li><strong>Come back and press Connect.</strong> Your NFTs and balance show up on your profile.</li>
    </ol>
    <div class="nw-links">
      {#each WALLET_LINKS as w (w.name)}
        <a class="nw-link" href={w.href} target="_blank" rel="noopener" onclick={(e) => install(e, w.name, w.href)}>
          <span class="nw-link-name">{w.name} <Icon name="external" size={14} /></span>
          <span class="nw-link-blurb">{w.blurb}</span>
        </a>
      {/each}
    </div>
    {#if blocked}
      {@const w = WALLET_LINKS.find((x) => x.name === blocked)}
      <p class="nw-note" role="status">Your browser blocked the pop-up. <a href={w?.href} target="_blank" rel="noopener">Open {blocked} in a new tab</a>.</p>
    {:else if opened}
      <p class="nw-note" role="status">{opened} opened in a new tab. Come back here and press Connect when it's set up.</p>
    {/if}
    <div class="nw-actions">
      <button type="button" class="btn btn-primary btn-lg" onclick={connectNow}>I have one now — Connect</button>
      <button type="button" class="btn btn-ghost" onclick={close}>Not now</button>
    </div>
  </div>
{/if}

<style>
  .nw-scrim { position: fixed; inset: 0; background: rgba(0,0,0,.6); z-index: var(--z-modal); backdrop-filter: blur(2px); }
  .nw-sheet { position: fixed; left: 0; right: 0; bottom: 0; z-index: calc(var(--z-modal) + 1); background: var(--surface); color: var(--text); border-top: 1px solid var(--line-strong); border-radius: 20px 20px 0 0; padding: var(--sp-4) var(--sp-4) calc(var(--sp-6) + env(safe-area-inset-bottom)); box-shadow: var(--shadow); max-height: 92vh; overflow-y: auto; }
  @media (min-width: 480px) {
    .nw-sheet { left: 50%; right: auto; bottom: auto; top: 50%; transform: translate(-50%, -50%); width: min(460px, calc(100vw - 32px)); border-radius: var(--r-card); border: 1px solid var(--line-strong); padding: var(--sp-6); }
  }
  .nw-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--sp-3); }
  h2 { font-size: var(--fs-h2); line-height: var(--lh-h2); font-weight: 800; margin: 0 0 var(--sp-3); }
  .nw-lines { margin: 0 0 var(--sp-4); padding-left: 1.25rem; display: flex; flex-direction: column; gap: var(--sp-2); font-size: var(--fs-body); line-height: var(--lh-body); color: var(--text-2); }
  .nw-lines strong { color: var(--text); }
  .nw-links { display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  @media (max-width: 400px) { .nw-links { grid-template-columns: 1fr; } }
  .nw-link { display: flex; flex-direction: column; gap: 2px; min-height: var(--hit); padding: var(--sp-3); border-radius: var(--r-control); border: 1px solid var(--line-strong); background: var(--surface-2); color: var(--text); text-decoration: none; transition: border-color var(--dur-fast) var(--ease); }
  .nw-link:hover { border-color: var(--sky); text-decoration: none; }
  .nw-link-name { font-weight: 700; display: inline-flex; align-items: center; gap: 4px; }
  .nw-link-blurb { font-size: var(--fs-small); color: var(--text-3); }
  .nw-note { margin: 0 0 var(--sp-3); font-size: var(--fs-small); line-height: var(--lh-small); color: var(--text-2); }
  .nw-note a { color: var(--link-strong); text-decoration: underline; text-underline-offset: 3px; }
  .nw-actions { display: flex; flex-direction: column; gap: var(--sp-2); }
</style>
