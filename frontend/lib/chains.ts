import {defineChain} from "viem";
import {CHAIN_ID, RPC_URL, EXPLORER_URL, CURRENCY_SYMBOL, CHAIN_NAME} from "./addresses";

export const coston2 = defineChain({
  id: CHAIN_ID,
  name: CHAIN_NAME,
  nativeCurrency: {name: CURRENCY_SYMBOL, symbol: CURRENCY_SYMBOL, decimals: 18},
  rpcUrls: {default: {http: [RPC_URL]}},
  blockExplorers: {default: {name: "Explorer", url: EXPLORER_URL}},
  testnet: CHAIN_ID !== 14
});
