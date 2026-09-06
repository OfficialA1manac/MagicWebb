// First-run strip + no-wallet path (v3.6 wave 5c): pure helpers shared by
// FirstRun.svelte (home hero column, token/auction/collection strip) and the
// unit tests. No Svelte, no DOM.

export interface StripStep { n: 1 | 2 | 3; label: string; done: boolean; href?: string; /** Opens the in-app no-wallet sheet. */ noWallet?: boolean }

/** Three steps; step 2 links the faucet on testnets. Step 1 carries the no-wallet affordance while disconnected. */
export function firstRunSteps(i: { connected: boolean; testnet: boolean; faucetUrl?: string | null; traded?: boolean }): StripStep[] {
  return [
    { n: 1, label: 'Connect your wallet', done: i.connected, noWallet: !i.connected },
    i.testnet
      ? { n: 2, label: 'Get free test FLR', done: false, href: i.faucetUrl ?? undefined }
      : { n: 2, label: 'Fund your wallet with FLR', done: false },
    { n: 3, label: 'Buy or list your first NFT', done: !!i.traded },
  ];
}

export type StripMode = 'fresh' | 'progress' | 'done' | 'hidden';

export interface StripView {
  visible: boolean;
  /** Dismiss control shows only once everything is done. */
  dismissible: boolean;
  /** Which step is highlighted as "next" (0 = none). */
  current: 0 | 1 | 2 | 3;
  mode: StripMode;
}

/**
 * Strip state table (plan §Interaction states → FirstRun strip):
 *   fresh    → 3 steps, step 1 highlighted, no dismiss
 *   progress → ✓ on done steps, next step highlighted
 *   done     → "You're set" + Dismiss
 *   hidden   → dismissed (a "Show steps" link brings it back)
 */
export function stripState(i: { dismissed: boolean; traded: boolean; connected?: boolean }): StripView {
  if (i.dismissed) return { visible: false, dismissible: true, current: 0, mode: 'hidden' };
  if (i.traded) return { visible: true, dismissible: true, current: 0, mode: 'done' };
  if (i.connected) return { visible: true, dismissible: false, current: 3, mode: 'progress' };
  return { visible: true, dismissible: false, current: 1, mode: 'fresh' };
}

export const STRIP_DISMISSED_KEY = 'mw-firstrun-dismissed';
export const FIRST_TRADE_KEY = 'mw-first-trade-done';
/** Window event fired whenever the strip's persisted state changes (dismiss / show). */
export const FIRSTRUN_CHANGED_EVENT = 'mw-firstrun-changed';
/** Window event that opens the no-wallet sheet (vanilla scripts + React + Svelte all dispatch it). */
export const NOWALLET_OPEN_EVENT = 'mw-nowallet-open';

/** Wallet install links shown in the no-wallet sheet. Both support Flare networks. */
export const WALLET_LINKS = [
  { name: 'MetaMask', href: 'https://metamask.io/download/', blurb: 'Browser extension and mobile app' },
  { name: 'Bifrost', href: 'https://bifrostwallet.com/', blurb: 'Made for Flare and Songbird' },
] as const;

export function openNoWalletSheet(): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(NOWALLET_OPEN_EVENT));
}
