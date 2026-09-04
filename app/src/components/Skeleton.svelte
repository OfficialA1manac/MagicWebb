<script lang="ts">
  // One skeleton: gradient sweep, 1.2s. `card` = image block + 3 lines.
  let { w = '100%', h = '14px', r = '8px', square = false, card = false }: { w?: string; h?: string; r?: string; square?: boolean; card?: boolean } = $props();
</script>

{#if card}
  <div class="sk-card" aria-hidden="true">
    <div class="sk sk-img"></div>
    <div class="sk" style="width:70%;height:14px"></div>
    <div class="sk" style="width:45%;height:12px"></div>
    <div class="sk" style="width:35%;height:16px"></div>
  </div>
{:else}
  <div class="sk" style="width:{w};height:{square ? 'auto' : h};border-radius:{r};{square ? 'aspect-ratio:1;' : ''}" aria-hidden="true"></div>
{/if}

<style>
  .sk { background: linear-gradient(90deg, var(--surface-2) 0%, rgba(255,255,255,.08) 50%, var(--surface-2) 100%); background-size: 200% 100%; animation: sk-sweep 1.2s linear infinite; border-radius: var(--r-control); }
  .sk-card { display: flex; flex-direction: column; gap: 10px; padding: 12px; border: 1px solid var(--line); border-radius: var(--r-card); background: var(--surface); }
  .sk-img { aspect-ratio: 1; width: 100%; border-radius: var(--r-control); }
  @keyframes sk-sweep { to { background-position: -200% 0; } }
  @media (prefers-reduced-motion: reduce) { .sk { animation: none; background: var(--surface-2); } }
</style>
