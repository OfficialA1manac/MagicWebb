<script lang="ts">
  let { message = 'Could not load this right now.', retry }: { message?: string; retry?: () => void } = $props();
</script>

<div class="err" role="alert">
  <div class="err-title">Something went wrong</div>
  <p>{message}</p>
  {#if retry}<button class="err-btn" onclick={retry}>Try again</button>{/if}
</div>

<style>
  .err { background: rgba(248,113,113,.08); border: 1px solid rgba(248,113,113,.3); border-radius: 14px; padding: 16px; font-size: 14px; color: rgba(255,255,255,.8); }
  .err-title { font-weight: 700; color: #fca5a5; margin-bottom: 4px; }
  .err p { margin: 0 0 10px; line-height: 1.5; }
  .err-btn { min-height: 40px; padding: 0 14px; border-radius: 10px; background: rgba(255,255,255,.08); color: #fafafa; border: 1px solid rgba(255,255,255,.14); cursor: pointer; font-family: inherit; font-weight: 700; }
</style>
