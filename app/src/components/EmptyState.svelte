<script lang="ts">
  import type { Snippet } from 'svelte';
  let { title, body = '', cta, children }: { title: string; body?: string; cta?: { label: string; href?: string; onclick?: () => void }; children?: Snippet } = $props();
</script>

<div class="es" role="status">
  <div class="es-glyph" aria-hidden="true">◻</div>
  <div class="es-title">{title}</div>
  {#if body}<p class="es-body">{body}</p>{/if}
  {#if cta}
    {#if cta.href}<a class="es-cta" href={cta.href}>{cta.label}</a>{:else}<button class="es-cta" onclick={cta.onclick}>{cta.label}</button>{/if}
  {/if}
  {@render children?.()}
</div>

<style>
  .es { border: 1px dashed rgba(255,255,255,.14); border-radius: 16px; padding: 28px 16px; text-align: center; color: rgba(255,255,255,.6); display: flex; flex-direction: column; gap: 10px; align-items: center; }
  .es-glyph { font-size: 28px; color: rgba(255,255,255,.25); }
  .es-title { font-weight: 700; color: #fafafa; font-size: 15px; }
  .es-body { font-size: 13px; max-width: 32rem; margin: 0; line-height: 1.5; }
  .es-cta { min-height: 44px; padding: 0 18px; border-radius: 12px; background: linear-gradient(135deg,#7dd3fc,#0ea5e9); color: #09090b; font-weight: 700; border: 0; cursor: pointer; font-family: inherit; display: inline-flex; align-items: center; text-decoration: none; font-size: 14px; }
</style>
