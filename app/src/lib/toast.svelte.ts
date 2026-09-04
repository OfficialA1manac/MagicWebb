// Global toast store (Svelte 5 runes, module-level). <Toasts/> in MwRuntime
// renders it; page scripts reach it through window.MW.toast (lib/mw.ts) or
// the `mw-toast` CustomEvent so string-built pages need no import.
//
// Rules (spec B0): max 3 stacked, 4s auto-dismiss (errors 8s + manual close),
// role=status aria-live=polite, variants success/info/error.

export type ToastVariant = 'success' | 'info' | 'error';

export interface Toast {
  id: number;
  variant: ToastVariant;
  message: string;
  /** Optional action link/button. */
  action?: { label: string; href?: string; onclick?: () => void };
  /** ms; 0 = manual close only. */
  duration: number;
}

export interface ToastOptions {
  variant?: ToastVariant;
  duration?: number;
  action?: Toast['action'];
}

export const MAX_TOASTS = 3;
export const DEFAULT_MS = 4000;
export const ERROR_MS = 8000;

export const toasts = $state<Toast[]>([]);

let seq = 0;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

export function dismissToast(id: number): void {
  const t = timers.get(id);
  if (t) { clearTimeout(t); timers.delete(id); }
  const i = toasts.findIndex((x) => x.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

export function clearToasts(): void {
  for (const id of [...timers.keys()]) dismissToast(id);
  toasts.splice(0, toasts.length);
}

/** Push a toast; returns its id. */
export function toast(message: string, opts: ToastOptions = {}): number {
  const variant = opts.variant ?? 'info';
  const duration = opts.duration ?? (variant === 'error' ? ERROR_MS : DEFAULT_MS);
  const id = ++seq;
  // Oldest out when the stack is full.
  while (toasts.length >= MAX_TOASTS) dismissToast(toasts[0].id);
  toasts.push({ id, variant, message, action: opts.action, duration });
  if (duration > 0) timers.set(id, setTimeout(() => dismissToast(id), duration));
  return id;
}

export const toastSuccess = (m: string, o: Omit<ToastOptions, 'variant'> = {}) => toast(m, { ...o, variant: 'success' });
export const toastInfo = (m: string, o: Omit<ToastOptions, 'variant'> = {}) => toast(m, { ...o, variant: 'info' });
export const toastError = (m: string, o: Omit<ToastOptions, 'variant'> = {}) => toast(m, { ...o, variant: 'error' });

/**
 * Bridge for inline page scripts: dispatch
 *   window.dispatchEvent(new CustomEvent('mw-toast', { detail: { message, variant } }))
 * Installed once by <Toasts/>.
 */
export function installToastBridge(): () => void {
  if (typeof window === 'undefined') return () => {};
  const on = (e: Event) => {
    const d = (e as CustomEvent<{ message?: string; variant?: ToastVariant; duration?: number }>).detail || {};
    if (d.message) toast(String(d.message), { variant: d.variant, duration: d.duration });
  };
  window.addEventListener('mw-toast', on);
  return () => window.removeEventListener('mw-toast', on);
}
