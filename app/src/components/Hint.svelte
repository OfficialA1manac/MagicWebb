<script lang="ts">
  // Tooltip/popover that works on touch: a 20px `i` button (44px hit area)
  // with aria-describedby; opens on click or focus, closes on Escape / outside
  // click / blur. Never `title=` only.
  import Icon from './Icon.svelte';
  let { text, label = 'More information', align = 'center' }: { text: string; label?: string; align?: 'start' | 'center' | 'end' } = $props();
  let open = $state(false);
  let root: HTMLSpanElement | undefined = $state();
  const id = `hint-${Math.random().toString(36).slice(2, 9)}`;

  function onDoc(e: MouseEvent) { if (root && !root.contains(e.target as Node)) open = false; }
  function onKey(e: KeyboardEvent) { if (e.key === 'Escape') { open = false; } }
  $effect(() => {
    if (!open) return;
    document.addEventListener('click', onDoc, true);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('click', onDoc, true); document.removeEventListener('keydown', onKey); };
  });
</script>

<span class="hint" bind:this={root}>
  <button type="button" class="hint-btn" aria-label={label} aria-describedby={id} aria-expanded={open}
          onclick={() => (open = !open)} onfocus={() => (open = true)}
          onblur={(e) => { if (!root?.contains(e.relatedTarget as Node)) open = false; }}>
    <Icon name="info" size={20} />
  </button>
  <span class="hint-pop align-{align}" role="tooltip" {id} hidden={!open}>{text}</span>
</span>

<style>
  .hint { position: relative; display: inline-flex; vertical-align: middle; }
  .hint-btn { width: var(--hit); height: var(--hit); margin: -12px; display: inline-flex; align-items: center; justify-content: center; border-radius: var(--r-pill); background: transparent; border: 0; color: var(--text-3); cursor: pointer; }
  .hint-btn:hover, .hint-btn[aria-expanded="true"] { color: var(--text); }
  .hint-pop { position: absolute; bottom: calc(100% + 6px); z-index: var(--z-modal); width: max-content; max-width: min(280px, 80vw); padding: var(--sp-2) var(--sp-3); border-radius: var(--r-control); background: var(--surface-2); border: 1px solid var(--line-strong); box-shadow: var(--shadow); color: var(--text); font-size: var(--fs-small); line-height: var(--lh-small); font-weight: 500; text-align: left; white-space: normal; }
  .align-center { left: 50%; transform: translateX(-50%); }
  .align-start { left: 0; }
  .align-end { right: 0; }
</style>
