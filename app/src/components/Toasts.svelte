<script lang="ts">
  // Toast host: bottom centre (above the tab bar on mobile), z-index toast.
  import { onMount } from 'svelte';
  import { toasts, dismissToast, installToastBridge } from '../lib/toast.svelte';
  import Icon from './Icon.svelte';

  onMount(() => installToastBridge());
  const icon = (v: string) => (v === 'success' ? 'check' : v === 'error' ? 'alert' : 'info');
</script>

<div class="toasts" role="status" aria-live="polite" aria-atomic="false">
  {#each toasts as t (t.id)}
    <div class="toast is-{t.variant}">
      <span class="toast-ico"><Icon name={icon(t.variant)} size={18} /></span>
      <span class="toast-msg">{t.message}</span>
      {#if t.action}
        {#if t.action.href}<a class="toast-act" href={t.action.href}>{t.action.label}</a>
        {:else}<button class="toast-act" onclick={() => { t.action?.onclick?.(); dismissToast(t.id); }}>{t.action.label}</button>{/if}
      {/if}
      <button class="toast-x" aria-label="Dismiss" onclick={() => dismissToast(t.id)}><Icon name="x" size={16} /></button>
    </div>
  {/each}
</div>

<style>
  .toasts { position: fixed; left: 50%; bottom: calc(var(--sp-4) + env(safe-area-inset-bottom)); transform: translateX(-50%); z-index: var(--z-toast); display: flex; flex-direction: column; gap: var(--sp-2); width: min(420px, calc(100vw - 2 * var(--sp-4))); pointer-events: none; }
  @media (max-width: 767px) { .toasts { bottom: calc(var(--tabbar-h) + var(--sp-3) + env(safe-area-inset-bottom)); } }
  .toast { pointer-events: auto; display: flex; align-items: center; gap: var(--sp-3); min-height: var(--hit); padding: var(--sp-2) var(--sp-2) var(--sp-2) var(--sp-3); border-radius: var(--r-card); background: var(--surface-2); border: 1px solid var(--line-strong); box-shadow: var(--shadow); color: var(--text); font-size: var(--fs-small); line-height: var(--lh-small); font-weight: 600; animation: toast-in var(--dur) var(--ease) both; }
  .toast-ico { display: inline-flex; flex: 0 0 auto; }
  .is-success .toast-ico { color: var(--green); }
  .is-info .toast-ico { color: var(--sky); }
  .is-error .toast-ico { color: var(--red); }
  .is-error { border-color: var(--red-35); }
  .toast-msg { flex: 1 1 auto; min-width: 0; }
  .toast-act { flex: 0 0 auto; min-height: 32px; padding: 0 var(--sp-2); border-radius: var(--r-control); background: transparent; border: 1px solid var(--line-strong); color: var(--text); font: inherit; font-weight: 700; cursor: pointer; text-decoration: none; display: inline-flex; align-items: center; }
  .toast-x { flex: 0 0 auto; width: 32px; height: 32px; display: inline-flex; align-items: center; justify-content: center; border-radius: var(--r-control); background: transparent; color: var(--text-3); border: 0; cursor: pointer; }
  .toast-x:hover { color: var(--text); background: rgba(255,255,255,.08); }
  @keyframes toast-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
</style>
