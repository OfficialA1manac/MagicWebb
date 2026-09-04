<script lang="ts">
  // Inline Lucide icon (lib/icons.ts). Decorative by default; pass `label`
  // for a standalone meaningful icon.
  import { iconPaths, type IconName } from '../lib/icons';
  let { name, size = 20, label = '', class: cls = '' }: { name: IconName; size?: number; label?: string; class?: string } = $props();
</script>

<svg class={cls} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
     stroke-linecap="round" stroke-linejoin="round" role={label ? 'img' : undefined} aria-label={label || undefined}
     aria-hidden={label ? undefined : 'true'} focusable="false">
  {@html iconPaths(name)}
</svg>
