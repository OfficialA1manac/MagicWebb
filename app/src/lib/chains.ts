// Per-network static metadata for the frontend. Mirrors the Go side
// (backend/internal/config — P2 moves it into internal/chain/profile and
// generates this table from the same source).
//
// Runtime values win: the Go server injects window.MW_* before </head>
// (cmd/server/ui.go astroConfigScript), so one Astro build serves every
// network. Build-time PUBLIC_* env is the dev fallback.
import type { Address } from 'viem';

// Runtime globals injected by cmd/server/ui.go (A2.8). Declared here so every
// consumer shares one type; env.d.ts carries the older MW_* set.
declare global {
  interface Window {
    /** 'live' | 'browse-only' — trading status of THIS deployment. */
    MW_TRADING?: string;
    /** Faucet URL (testnets only). */
    MW_FAUCET_URL?: string;
    /** JSON array of { chainId, name, origin, status: 'trading'|'browse-only', testnet } for every network. */
    MW_NETWORK_STATUS_JSON?: string;
  }
}

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
 * (a network before its contracts deploy): browsing, wallet and profile work,
 * every trade surface points at a live sibling origin instead.
 *
 * v3.4 hardening: requires ALL THREE core addresses, not just the
 * marketplace — a partial config must not light up auction/offer buttons
 * that would then throw in the tx layer.
 */
export function tradingLive(): boolean {
  if (typeof window !== 'undefined' && window.MW_TRADING === 'browse-only') return false;
  const c = currentChain().contracts;
  return c.marketplace !== null && c.auctionHouse !== null && c.offerBook !== null;
}

/** Short display name for the header pill / switcher (never "Flare Coston2"). */
export function shortChainName(id: number): string {
  return ({ 114: 'Coston2', 19: 'Songbird', 14: 'Flare' } as Record<number, string>)[id] ?? `Chain ${id}`;
}

export function isTestnet(id: number): boolean { return id === 114; }

/** Faucet for the current testnet; null on mainnets. */
export function faucetUrl(): string | null {
  const id = currentChain().id;
  if (!isTestnet(id)) return null;
  const w = typeof window !== 'undefined' ? window : ({} as Window);
  return w.MW_FAUCET_URL || 'https://faucet.flare.network/coston2';
}

export interface NetworkStatus {
  chainId: number;
  name: string;
  origin: string;
  trading: boolean;
  testnet: boolean;
  current: boolean;
}

/**
 * Every network with its trading status, from MW_NETWORK_STATUS_JSON; falls
 * back to MW_NETWORK_URLS + static names (status = live only for ourselves
 * when tradingLive()). Tolerant of field aliases while the backend contract
 * settles: chainId|chain_id|id, origin|url, status|trading.
 */
export function networkStatuses(): NetworkStatus[] {
  const self = currentChain().id;
  const raw = typeof window !== 'undefined' ? window.MW_NETWORK_STATUS_JSON : undefined;
  const out: NetworkStatus[] = [];
  if (raw) {
    try {
      const parsed = JSON.parse(raw) as unknown;
      const list: Record<string, unknown>[] = Array.isArray(parsed)
        ? (parsed as Record<string, unknown>[])
        : Object.entries(parsed as Record<string, Record<string, unknown>>).map(([k, v]) => ({ chainId: Number(k), ...v }));
      for (const n of list) {
        const id = Number(n.chainId ?? n.chain_id ?? n.id);
        if (!Number.isFinite(id)) continue;
        const st = n.status ?? n.trading;
        const trading = st === true || st === 'trading' || st === 'live';
        out.push({
          chainId: id,
          name: String(n.name ?? shortChainName(id)),
          origin: String(n.origin ?? n.url ?? '').replace(/\/+$/, ''),
          trading,
          testnet: typeof n.testnet === 'boolean' ? n.testnet : isTestnet(id),
          current: id === self,
        });
      }
    } catch { /* fall through to URL fallback */ }
  }
  if (out.length === 0) {
    const m = networkOrigins();
    for (const id of [114, 19, 14]) {
      const origin = m.get(id) ?? '';
      if (!origin && id !== self) continue;
      out.push({ chainId: id, name: shortChainName(id), origin, trading: id === self ? tradingLive() : id === 114, testnet: isTestnet(id), current: id === self });
    }
  }
  return out;
}

/**
 * Preference order for read-only-mode CTAs. Only consulted on a deployment
 * that is itself read-only; once every network trades, no surface reads it.
 */
const TRADING_CTA_PREF = [114, 14, 19];

/** Origin of the preferred sibling trading network, for read-only-mode CTAs. */
export function tradingOrigin(): string | null {
  const m = networkOrigins();
  for (const id of TRADING_CTA_PREF) {
    if (id === currentChain().id) continue; // never CTA to ourselves
    const o = m.get(id);
    if (o) return o;
  }
  return null;
}

/** Display name for the network tradingOrigin() points at. */
export function tradingOriginName(): string {
  const m = networkOrigins();
  for (const id of TRADING_CTA_PREF) {
    if (id === currentChain().id) continue;
    if (m.get(id)) return chainName(id);
  }
  return chainName(114);
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
    cta: `Trade on ${tradingOriginName()}`,
    ctaHref: tradingOrigin(),
  };
}
