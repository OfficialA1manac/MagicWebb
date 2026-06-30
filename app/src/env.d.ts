/// <reference types="astro/client" />
/// <reference types="vite/client" />

// ── Server-injected window globals ──────────────────────────────────────────
// The Go backend injects these via layout.html / Astro page templates so the
// same frontend build works across Coston2 (chain 114) and mainnet (chain 14).
// Exposed as `window.MW_*` globals that the WalletConnect component reads to
// configure the Reown AppKit without a rebuild.
interface Window {
  /** Chain ID injected by the Go backend (e.g. 114 for Coston2, 14 for mainnet). */
  MW_CHAIN_ID?: string;
  /** RPC URL for the current chain. */
  MW_RPC_URL?: string;
  /** Human-readable network name (e.g. "Flare Coston2"). */
  MW_NETWORK_NAME?: string;
  /** Native currency symbol (e.g. "C2FLR"). */
  MW_NATIVE_CURRENCY?: string;
  /** Block explorer base URL. */
  MW_EXPLORER?: string;
  /** Reown / WalletConnect project ID. */
  MW_WC_PROJECT_ID?: string;

  // ── AppKit external trigger API ──────────────────────────────────────────
  // Set by WalletConnect component so the mobile menu / page chrome can open
  // the AppKit modal or disconnect without importing the component.
  __MW_APPKIT_OPEN__?: () => void;
  __MW_APPKIT_DISCONNECT__?: () => void;
  __MW_APPKIT_READY__?: boolean;
}
