// Per-network static metadata for the frontend. Mirrors the Go side
// (backend/internal/config — P2 moves it into internal/chain/profile and
// generates this table from the same source).
//
// Runtime values win: the Go server injects window.MW_* before </head>
// (cmd/server/ui.go astroConfigScript), so one Astro build serves every
// network. Build-time PUBLIC_* env is the dev fallback.
import type { Address } from 'viem';

export type ChainKey = 'coston2' | 'songbird' | 'flare';

export interface ChainProfile {
  id: 114 | 19 | 14;
  key: ChainKey;
  name: string;
  currency: string;
  explorer: string;
  rpc: string;
  /** Receipt confirmations the UI waits for before calling a tx "Done". */
  confirmations: number;
  /** Typical block interval, for the ETA shown while pending. */
  blockTimeMs: number;
  contracts: {
    marketplace: Address | null;
    auctionHouse: Address | null;
    offerBook: Address | null;
  };
}

const STATIC: Record<number, Omit<ChainProfile, 'rpc' | 'contracts' | 'name' | 'currency' | 'explorer'> & { name: string; currency: string; explorer: string; rpc: string }> = {
  114: { id: 114, key: 'coston2', name: 'Flare Coston2', currency: 'C2FLR', explorer: 'https://coston2-explorer.flare.network', rpc: 'https://coston2-api.flare.network/ext/C/rpc', confirmations: 1, blockTimeMs: 1800 },
  19: { id: 19, key: 'songbird', name: 'Songbird', currency: 'SGB', explorer: 'https://songbird-explorer.flare.network', rpc: 'https://songbird-api.flare.network/ext/C/rpc', confirmations: 1, blockTimeMs: 1800 },
  14: { id: 14, key: 'flare', name: 'Flare', currency: 'FLR', explorer: 'https://flare-explorer.flare.network', rpc: 'https://flare-api.flare.network/ext/C/rpc', confirmations: 1, blockTimeMs: 1800 },
};

function addr(v: string | undefined): Address | null {
  return v && /^0x[0-9a-fA-F]{40}$/.test(v) ? (v as Address) : null;
}

function env(name: string): string | undefined {
  try { return (import.meta.env as Record<string, string | undefined>)[name]; } catch { return undefined; }
}

let cached: ChainProfile | null = null;

/** The network this deployment serves. One origin == one chain. */
export function currentChain(): ChainProfile {
  if (cached) return cached;
  const w = typeof window !== 'undefined' ? window : ({} as Window);
  const id = Number(w.MW_CHAIN_ID || env('PUBLIC_CHAIN_ID') || 114);
  // Fail closed on an unsupported chain id: display metadata falls back to
  // Coston2 (runtime-injected values still win), but contract addresses are
  // dropped so tradingLive() is false and no transaction can be built against
  // addresses configured for a chain the wallet layer would not target.
  const known = STATIC[id] !== undefined;
  const base = STATIC[id] ?? STATIC[114];
  cached = {
    ...base,
    name: w.MW_NETWORK_NAME || base.name,
    currency: w.MW_NATIVE_CURRENCY || base.currency,
    explorer: (w.MW_EXPLORER || base.explorer).replace(/\/+$/, ''),
    rpc: w.MW_RPC_URL || env('PUBLIC_RPC_URL') || base.rpc,
    contracts: known ? {
      marketplace: addr(w.MW_MARKETPLACE) ?? addr(env('PUBLIC_MARKETPLACE_ADDR')),
      auctionHouse: addr(w.MW_AUCTION) ?? addr(env('PUBLIC_AUCTION_ADDR')),
      offerBook: addr(w.MW_OFFERBOOK) ?? addr(env('PUBLIC_OFFERBOOK_ADDR')),
    } : { marketplace: null, auctionHouse: null, offerBook: null },
  };
  return cached;
}

/** Test hook. */
export function _resetChainCache() { cached = null; }

export function explorerTx(hash: string): string { return `${currentChain().explorer}/tx/${hash}`; }
export function explorerAddress(a: string): string { return `${currentChain().explorer}/address/${a}`; }

/** Known sibling deployments from NETWORK_URLS (chainId=origin,…). */
export function networkOrigins(): Map<number, string> {
  const raw = (typeof window !== 'undefined' && window.MW_NETWORK_URLS) || env('PUBLIC_NETWORK_URLS') || '';
  const m = new Map<number, string>();
  for (const pair of raw.split(',')) {
    const [id, ...rest] = pair.split('=');
    const origin = rest.join('=').trim().replace(/\/+$/, '');
    const n = Number(id?.trim());
    if (Number.isFinite(n) && origin) m.set(n, origin);
  }
  return m;
}

export function chainName(id: number): string { return STATIC[id]?.name ?? `chain ${id}`; }

/**
 * Whether trading is live on this deployment. False = read-only network mode
 * (Songbird/Flare before contracts deploy): browsing, wallet and profile work,
 * every trade surface points at the Coston2 origin instead.
 */
export function tradingLive(): boolean {
  return currentChain().contracts.marketplace !== null;
}

/** Origin of the primary trading network, for read-only-mode CTAs. */
export function tradingOrigin(): string | null {
  return networkOrigins().get(114) ?? null;
}

/**
 * One copy source for every read-only trade surface (banner + empty states).
 * The CTA is a destination, not a "continue" — sessions do not cross origins.
 */
export function readOnlyCopy(): { heading: string; body: string; cta: string; ctaHref: string | null } {
  const name = currentChain().name;
  return {
    heading: `Trading isn't live on ${name} yet`,
    body: 'You can browse, connect your wallet, and view your profile. Trading opens after the security audit.',
    cta: 'Trade on Flare Coston2',
    ctaHref: tradingOrigin(),
  };
}
