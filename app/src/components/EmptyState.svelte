<script lang="ts">
  // Empty states are features: icon 32px, title 17/600, body 15 --text-2,
  // one primary action, optional secondary link.
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from '../lib/icons';
  let { title, body = '', icon = 'inbox', cta, secondary, children }: {
    title: string; body?: string; icon?: IconName;
    cta?: { label: string; href?: string; onclick?: () => void; disabled?: boolean; reason?: string };
    secondary?: { label: string; href: string };
    children?: Snippet;
  } = $props();
</script>

<div class="es" role="status">
  <div class="es-glyph"><Icon name={icon} size={32} /></div>
  <h3 class="es-title">{title}</h3>
  {#if body}<p class="es-body">{body}</p>{/if}
  {#if cta}
    {#if cta.href && !cta.disabled}<a class="btn btn-primary" href={cta.href}>{cta.label}</a>
    {:else}<button class="btn btn-primary" onclick={cta.onclick} aria-disabled={cta.disabled ? 'true' : undefined} disabled={cta.disabled} title={cta.disabled ? cta.reason : undefined}>{cta.label}</button>{/if}
  {/if}
  {#if secondary}<a class="es-secondary" href={secondary.href}>{secondary.label}</a>{/if}
  {@render children?.()}
</div>

<style>
  .es { border: 1px dashed var(--line-strong); border-radius: var(--r-card); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--text-2); display: flex; flex-direction: column; gap: var(--sp-3); align-items: center; }
  .es-glyph { color: var(--text-3); display: inline-flex; }
  .es-title { font-weight: 600; color: var(--text); font-size: var(--fs-h3); line-height: var(--lh-h3); margin: 0; }
  .es-body { font-size: var(--fs-body); line-height: var(--lh-body); color: var(--text-2); max-width: 32rem; margin: 0; }
  .es-secondary { font-size: var(--fs-small); color: var(--text-2); text-decoration: underline; text-underline-offset: 3px; min-height: var(--hit); display: inline-flex; align-items: center; }
  .es-secondary:hover { color: var(--text); }
</style>
