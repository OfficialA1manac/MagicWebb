import {defineChain} from "viem";
import {CHAIN_ID, RPC_URL} from "./addresses";

export const coston2 = defineChain({
  id: CHAIN_ID,
  name: "Flare Coston2",
  nativeCurrency: {name: "Coston2 Flare", symbol: "C2FLR", decimals: 18},
  rpcUrls: {default: {http: [RPC_URL]}},
  blockExplorers: {default: {name: "Routescan", url: "https://coston2-explorer.flare.network"}},
  testnet: true
});
