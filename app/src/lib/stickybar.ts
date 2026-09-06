// `--sticky-bar-h` (v3.6 wave 5c): the mobile sticky action bars on the
// token / auction / profile-batch pages publish their rendered height on
// <html> so the toast stack can sit above them (Toasts.svelte reads it).
// Zero whenever the bar is hidden (desktop) or unmounted.

export const STICKY_BAR_VAR = '--sticky-bar-h';

function visibleHeight(el: HTMLElement): number {
  if (typeof getComputedStyle === 'undefined') return 0;
  const cs = getComputedStyle(el);
  if (cs.display === 'none' || cs.visibility === 'hidden') return 0;
  return Math.round(el.getBoundingClientRect().height);
}

/**
 * Keep `--sticky-bar-h` equal to `el`'s visible height until the returned
 * cleanup runs (then it resets to 0). Uses ResizeObserver when available and
 * re-measures on viewport resize (the bars hide via media queries).
 */
export function bindStickyBarHeight(el: HTMLElement, root: HTMLElement = document.documentElement): () => void {
  const apply = () => root.style.setProperty(STICKY_BAR_VAR, `${visibleHeight(el)}px`);
  apply();
  let ro: ResizeObserver | null = null;
  if (typeof ResizeObserver !== 'undefined') { ro = new ResizeObserver(apply); ro.observe(el); }
  window.addEventListener('resize', apply);
  return () => {
    ro?.disconnect();
    window.removeEventListener('resize', apply);
    root.style.setProperty(STICKY_BAR_VAR, '0px');
  };
}
