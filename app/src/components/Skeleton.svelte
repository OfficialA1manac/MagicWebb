<script lang="ts">
  let { w = '100%', h = '14px', r = '8px', square = false }: { w?: string; h?: string; r?: string; square?: boolean } = $props();
</script>

<div class="sk" style="width:{w};height:{square ? 'auto' : h};border-radius:{r};{square ? 'aspect-ratio:1;' : ''}" aria-hidden="true"></div>

<style>
  .sk { background: linear-gradient(90deg,#15151a,#1f1f28,#15151a); background-size: 200% 100%; animation: sk 1.4s infinite; }
  @keyframes sk { to { background-position: -200% 0; } }
  @media (prefers-reduced-motion: reduce) { .sk { animation: none; background: #1a1a24; } }
</style>
