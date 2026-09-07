// Per-network static metadata for the frontend, one file per network
// (coston2.ts / songbird.ts / flare.ts) mirroring backend/internal/chain/profile.
// profile_test.go asserts the two stay in sync field by field.
//
// Runtime values win: the Go server injects window.MW_* before </head>
// (cmd/server/ui.go astroConfigScript), so one Astro build serves every
// network. Build-time PUBLIC_* env is the dev fallback.
import type { Address } from 'viem';
import type { StaticChain, ChainKey, ChainId } from './types';
import { coston2 } from './coston2';
import { songbird } from './songbird';
import { flare } from './flare';

export type { StaticChain, ChainKey, ChainId } from './types';
export { coston2, songbird, flare };

// Runtime globals injected by cmd/server/ui.go (A2.8 + v3.6). Declared here so
// every consumer shares one type; env.d.ts carries the older MW_* set.
declare global {
  interface Window {
    /** 'live' | 'browse-only' — trading status of THIS deployment. */
    MW_TRADING?: string;
    /** Faucet URL (testnets only). */
    MW_FAUCET_URL?: string;
    /** JSON array of { chainId, name, origin, status: 'trading'|'browse-only', testnet } for every network. */
    MW_NETWORK_STATUS_JSON?: string;
    /** Profile BlockTime in ms (v3.6): drives the "~N s" ETA while a tx is pending. */
    MW_BLOCK_TIME_MS?: string;
    /** Profile Confirmations (v3.6). */
    MW_CONFIRMATIONS?: string;
    /** 'true' | 'false' from the profile's Mainnet flag (v3.6); the server, not a chain-id check, decides. */
    MW_TESTNET?: string;
  }
}

export interface ChainProfile extends StaticChain {
  contracts: {
    marketplace: Address | null;
    auctionHouse: Address | null;
    offerBook: Address | null;
  };
}

/** Every supported network, in switcher order (114, 19, 14). */
export const STATIC: Record<number, StaticChain> = {
  [coston2.id]: coston2,
  [songbird.id]: songbird,
  [flare.id]: flare,
};

function addr(v: string | undefined): Address | null {
  return v && /^0x[0-9a-fA-F]{40}$/.test(v) ? (v as Address) : null;
}

function env(name: string): string | undefined {
  try { return (import.meta.env as Record<string, string | undefined>)[name]; } catch { return undefined; }
}

function num(v: string | undefined, fallback: number): number {
  const n = Number(v);
  return v !== undefined && v !== '' && Number.isFinite(n) && n > 0 ? n : fallback;
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
  const base = STATIC[id] ?? coston2;
  cached = {
    ...base,
    name: w.MW_NETWORK_NAME || base.name,
    currency: w.MW_NATIVE_CURRENCY || base.currency,
    explorer: (w.MW_EXPLORER || base.explorer).replace(/\/+$/, ''),
    rpc: w.MW_RPC_URL || env('PUBLIC_RPC_URL') || base.rpc,
    blockTimeMs: num(w.MW_BLOCK_TIME_MS, base.blockTimeMs),
    confirmations: num(w.MW_CONFIRMATIONS, base.confirmations),
    testnet: w.MW_TESTNET === 'true' ? true : w.MW_TESTNET === 'false' ? false : base.testnet,
    faucet: w.MW_FAUCET_URL || base.faucet,
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

/**
 * Seconds a transaction typically needs before the UI can call it done:
 * blockTime × confirmations, rounded up (1.8 s × 1 → "~2 s").
 */
export function pendingEtaSeconds(c: Pick<ChainProfile, 'blockTimeMs' | 'confirmations'> = currentChain()): number {
  return Math.max(1, Math.ceil((c.blockTimeMs * Math.max(1, c.confirmations)) / 1000));
}

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

/**
 * Testnet by profile: for the current chain the server-injected MW_TESTNET
 * wins; for any other id the static table answers.
 */
export function isTestnet(id: number): boolean {
  const cur = currentChain();
  if (id === cur.id) return cur.testnet;
  return STATIC[id]?.testnet ?? false;
}

/** Faucet for the current testnet; null on mainnets. */
export function faucetUrl(): string | null {
  const c = currentChain();
  if (!c.testnet) return null;
  return c.faucet || 'https://faucet.flare.network/coston2';
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
