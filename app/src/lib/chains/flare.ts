import type { StaticChain } from './types';

// Flare mainnet (chain 14). Mirror of
// backend/internal/chain/profile/flare.go — profile_test.go asserts parity.
export const flare: StaticChain = {
  id: 14,
  key: 'flare',
  name: 'Flare',
  currency: 'FLR',
  explorer: 'https://flare-explorer.flare.network',
  rpc: 'https://flare-api.flare.network/ext/C/rpc',
  rpcFallbacks: ['https://rpc.ankr.com/flare', 'https://flare.rpc.thirdweb.com'],
  confirmations: 1,
  blockTimeMs: 1800,
  testnet: false,
  faucet: null,
};
