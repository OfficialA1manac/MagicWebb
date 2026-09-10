<script lang="ts">
  import { DURATIONS, DEFAULT_DURATION } from '../lib/tx/durations';
  let { value = $bindable(DEFAULT_DURATION), label = 'Duration' }: { value?: number; label?: string } = $props();

  // ARIA radiogroup means ONE tab stop with arrow keys inside it, not one tab
  // stop per option. Every option was natively focusable, so a keyboard user
  // had to tab through all 15 durations to reach the next field, and arrow
  // keys did nothing at all.
  let selectedIndex = $derived(Math.max(0, DURATIONS.findIndex((d) => d.seconds === value)));

  function onKey(e: KeyboardEvent, i: number) {
    let next = -1;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = (i + 1) % DURATIONS.length;
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = (i - 1 + DURATIONS.length) % DURATIONS.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = DURATIONS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    // Selection follows focus, which is the expected radiogroup behaviour.
    value = DURATIONS[next].seconds;
    const row = (e.currentTarget as HTMLElement).parentElement;
    (row?.children[next] as HTMLElement | undefined)?.focus();
  }
</script>

<fieldset class="dp">
  <legend>{label}</legend>
  <div class="dp-row" role="radiogroup" aria-label={label}>
    {#each DURATIONS as d, i (d.seconds)}
      <button
        type="button"
        class="dp-opt"
        class:is-on={value === d.seconds}
        role="radio"
        aria-checked={value === d.seconds}
        tabindex={i === selectedIndex ? 0 : -1}
        onkeydown={(e) => onKey(e, i)}
        onclick={() => (value = d.seconds)}>{d.label}</button>
    {/each}
  </div>
</fieldset>

<style>
  /* min-width: 0 — a fieldset defaults to min-inline-size: min-content, which
     stops the scrolling option row from ever shrinking below its full width
     (on a 390px token page the offer panel grew to 714px and the options past
     "1 hour" were unreachable). */
  .dp { border: 0; padding: 0; margin: 0; min-width: 0; }
  legend { font-size: 12px; color: var(--white-60); font-weight: 600; margin-bottom: 6px; }
  .dp-row { display: grid; grid-template-columns: repeat(auto-fill, minmax(72px, 1fr)); gap: 6px; }
  .dp-opt { min-height: var(--hit); padding: 0 8px; border-radius: 10px; background: var(--white-10); color: var(--text); border: 1px solid var(--white-10); font-family: inherit; font-size: 12px; font-weight: 600; cursor: pointer; white-space: nowrap; transition: background-color var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease); }
  .dp-opt.is-on { background: var(--sky-12); border-color: var(--sky); color: var(--text); }
  .dp-opt:focus-visible { outline: 2px solid var(--sky); outline-offset: 2px; }
  /* Phones: two rows that scroll sideways — every chip stays a full 44px thumb target. */
  @media (max-width: 640px) {
    .dp-row { grid-auto-flow: column; grid-template-columns: none; grid-template-rows: repeat(2, auto); grid-auto-columns: minmax(84px, max-content); overflow-x: auto; padding-bottom: 4px; scrollbar-width: thin; scroll-snap-type: x proximity; }
    .dp-opt { min-width: 84px; scroll-snap-align: start; }
  }
</style>
