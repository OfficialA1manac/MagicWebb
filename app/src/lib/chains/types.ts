export type ChainKey = 'coston2' | 'songbird' | 'flare';
export type ChainId = 114 | 19 | 14;

/** The identity half of backend/internal/chain/profile, one file per network. */
export interface StaticChain {
  id: ChainId;
  key: ChainKey;
  name: string;
  currency: string;
  explorer: string;
  /** Primary public RPC (profile DefaultRPCs[0]). */
  rpc: string;
  /** Rotation fallbacks (profile DefaultRPCs[1:]). */
  rpcFallbacks: string[];
  /** Receipt confirmations the UI waits for before calling a tx "Done". */
  confirmations: number;
  /** Typical block interval, for the ETA shown while pending. */
  blockTimeMs: number;
  /** No real value at stake: faucet link, testnet banner. */
  testnet: boolean;
  /** Where testers get gas; null on mainnets. */
  faucet: string | null;
}
