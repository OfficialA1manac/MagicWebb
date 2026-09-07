import type { StaticChain } from './types';

// Songbird canary network (chain 19). Mirror of
// backend/internal/chain/profile/songbird.go — profile_test.go asserts parity.
export const songbird: StaticChain = {
  id: 19,
  key: 'songbird',
  name: 'Songbird',
  currency: 'SGB',
  explorer: 'https://songbird-explorer.flare.network',
  rpc: 'https://songbird-api.flare.network/ext/C/rpc',
  rpcFallbacks: ['https://rpc.ankr.com/flare_songbird'],
  confirmations: 1,
  blockTimeMs: 1800,
  testnet: false,
  faucet: null,
};
