import {createConfig, http} from "wagmi";
import {injected, walletConnect} from "wagmi/connectors";
import {coston2} from "./chains";
import {RPC_URL} from "./addresses";

const wcProjectId = process.env.NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID?.trim() ?? "";
const appUrl = (process.env.NEXT_PUBLIC_APP_URL ?? "http://localhost:3000").replace(/\/$/, "");

// WalletConnect accesses localStorage during initialization; guard SSR.
const isClient = typeof window !== "undefined";

const connectors = [
  injected(),
  ...(wcProjectId && isClient
    ? [
        walletConnect({
          projectId: wcProjectId,
          showQrModal: true,
          metadata: {
            name: "MagicWebb",
            description: "Non-custodial NFT marketplace on Flare",
            url: appUrl,
            icons: []
          }
        })
      ]
    : [])
];

export const wagmiConfig = createConfig({
  chains: [coston2],
  connectors,
  transports: {[coston2.id]: http(RPC_URL, {batch: true})},
  ssr: true, // Required for Next.js App Router — prevents hydration mismatch
});
