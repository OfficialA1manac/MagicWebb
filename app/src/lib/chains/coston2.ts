import type { StaticChain } from './types';

// Flare Coston2 testnet (chain 114). Mirror of
// backend/internal/chain/profile/coston2.go — profile_test.go asserts parity.
export const coston2: StaticChain = {
  id: 114,
  key: 'coston2',
  name: 'Flare Coston2',
  currency: 'C2FLR',
  explorer: 'https://coston2-explorer.flare.network',
  rpc: 'https://coston2-api.flare.network/ext/C/rpc',
  rpcFallbacks: ['https://coston2.enosys.global/ext/C/rpc', 'https://rpc.ankr.com/flare_coston2'],
  confirmations: 1,
  blockTimeMs: 1800,
  testnet: true,
  faucet: 'https://faucet.flare.network/coston2',
};
