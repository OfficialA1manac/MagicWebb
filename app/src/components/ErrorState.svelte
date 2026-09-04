<script lang="ts">
  import Icon from './Icon.svelte';
  let { title = 'Something went wrong', message = 'Could not load this right now.', retry, retryLabel = 'Retry' }: { title?: string; message?: string; retry?: () => void; retryLabel?: string } = $props();
</script>

<div class="err" role="alert">
  <div class="err-head"><Icon name="alert" size={20} /><span class="err-title">{title}</span></div>
  <p>{message}</p>
  {#if retry}<button class="btn btn-secondary" onclick={retry}><Icon name="refresh" size={18} />{retryLabel}</button>{/if}
</div>

<style>
  .err { background: var(--red-12); border: 1px solid var(--red-35); border-radius: var(--r-card); padding: var(--sp-4); font-size: var(--fs-body); line-height: var(--lh-body); color: var(--text-2); }
  .err-head { display: flex; align-items: center; gap: var(--sp-2); color: var(--red); margin-bottom: var(--sp-1); }
  .err-title { font-weight: 700; color: var(--text); }
  .err p { margin: 0 0 var(--sp-3); }
</style>
