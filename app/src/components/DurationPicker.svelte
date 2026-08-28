<script lang="ts">
  import { DURATIONS, DEFAULT_DURATION } from '../lib/tx/durations';
  let { value = $bindable(DEFAULT_DURATION), label = 'Duration' }: { value?: number; label?: string } = $props();
</script>

<fieldset class="dp">
  <legend>{label}</legend>
  <div class="dp-row" role="radiogroup" aria-label={label}>
    {#each DURATIONS as d (d.seconds)}
      <button type="button" class="dp-opt" class:is-on={value === d.seconds} role="radio" aria-checked={value === d.seconds} onclick={() => (value = d.seconds)}>{d.label}</button>
    {/each}
  </div>
</fieldset>

<style>
  .dp { border: 0; padding: 0; margin: 0; }
  legend { font-size: 12px; color: rgba(255,255,255,.55); font-weight: 600; margin-bottom: 6px; }
  .dp-row { display: grid; grid-template-columns: repeat(auto-fill, minmax(72px, 1fr)); gap: 6px; }
  .dp-opt { min-height: 36px; padding: 0 8px; border-radius: 10px; background: rgba(255,255,255,.05); color: #fafafa; border: 1px solid rgba(255,255,255,.12); font-family: inherit; font-size: 12px; font-weight: 600; cursor: pointer; white-space: nowrap; }
  .dp-opt.is-on { background: rgba(125,211,252,.16); border-color: #7dd3fc; color: #e0f2fe; }
  .dp-opt:focus-visible { outline: 2px solid #7dd3fc; outline-offset: 2px; }
</style>
