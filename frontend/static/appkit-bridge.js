/* ── Reown AppKit bridge (ES module — loaded via <script type="module">).
 * Initialises AppKit with the ethers adapter so the built-in wallet-selection
 * modal (with QR code, deep links, and multiple wallet options) replaces the
 * previous custom wc-overlay.js approach.
 *
 * Fallback contract: if esm.sh is unreachable or the init fails, window.__MW_APPKIT__
 * stays undefined and wallet.js falls through to the self-hosted
 * @walletconnect/ethereum-provider UMD (wc-bundle.js) + wc-overlay.js.
 *
 * Exposes: window.__MW_APPKIT__ = { ready, connect, disconnect, onStateChange }
 *   ready          — Promise<boolean> resolves when init completes (true=ok, false=failed)
 *   connect()      — Promise<EIP1193Provider> opens modal, resolves on connect
 *   disconnect()   — tears down the WC session
 *   onStateChange  — callback(state) called on every state transition
 */

import { createAppKit } from 'https://esm.sh/@reown/appkit@1.8.21';
import { EthersAdapter } from 'https://esm.sh/@reown/appkit-adapter-ethers@1.8.21';

(function () {
'use strict';

let _appKit = null;
let _ethersAdapter = null;
let _ready = false;
let _initError = null;

const projectId = (window.MW_WC_PROJECT_ID || '').trim();
if (!projectId) {
  console.warn('[mw-appkit] No WC project ID — AppKit bridge disabled.');
  window.__MW_APPKIT__ = { ready: Promise.resolve(false), connect: null, disconnect: null, onStateChange: null };
  return;
}

const chainId = Number(window.MW_NETWORK_ID || 114);
const rpcUrl  = window.MW_RPC_URL || 'https://coston2-api.flare.network/ext/C/rpc';
const explorer = window.MW_EXPLORER || 'https://coston2-explorer.flare.network';
const networkName = window.MW_NETWORK_NAME || 'Flare Coston2';
const nativeCurrency = window.MW_NATIVE_CURRENCY || 'C2FLR';

/* ── Chain definitions (matches wallet.js server-injected config) ── */
const primaryChain = {
  id: chainId,
  name: networkName,
  nativeCurrency: { name: nativeCurrency, symbol: nativeCurrency, decimals: 18 },
  rpcUrls: { default: { http: [rpcUrl] } },
  blockExplorers: { default: { name: networkName + ' Explorer', url: explorer } },
};

// Only expose the primary chain (Coston2 or mainnet, per server config).
// wallet.js validates chainId post-connection and surfaces a toast if the
// wallet is on the wrong network — re-pairing is a single click. Keeping
// the chain list single avoids the confusing UX where AppKit shows two
// networks but the dApp only transacts on one.
const chains = [primaryChain];

/* ── Init AppKit ── */
async function init() {
  try {
    // EthersAdapter bridges AppKit's wallet-selection modal to ethers.js v6.
    // The adapter accepts the projectId so it can configure the internal
    // WalletConnect relay client. Passing projectId here keeps the adapter
    // in sync with createAppKit's own projectId (same value).
    _ethersAdapter = new EthersAdapter({ projectId: projectId });

    _appKit = createAppKit({
      adapters: [_ethersAdapter],
      networks: chains,
      defaultNetwork: primaryChain,
      projectId: projectId,
      metadata: {
        name: 'MagicWebb',
        description: 'Non-custodial NFT marketplace on ' + networkName,
        url: (typeof window !== 'undefined' && window.location && window.location.origin) || '',
        icons: [(typeof window !== 'undefined' && window.location && window.location.origin || '') + '/favicon.ico'],
      },
      features: {
        analytics: false,
        email: false,
        socials: false,
      },
      themeMode: 'dark',
      enableWalletSelector: true,
      enableNetworkSelector: false,
    });

    _ready = true;
    console.log('[mw-appkit] Reown AppKit bridge initialised (ethers adapter)');
    window.dispatchEvent(new CustomEvent('mw-appkit-ready'));
    return true;
  } catch (e) {
    _initError = e;
    console.error('[mw-appkit] Init failed — falling back to self-hosted WC bundle:', e.message || e);
    return false;
  }
}

/* ── Connect: opens AppKit modal, resolves with EIP-1193 provider ── */
function connect() {
  if (!_ready || !_appKit) {
    return Promise.reject(new Error('AppKit not initialised'));
  }

  return new Promise((resolve, reject) => {
    let resolved = false;

    const unsub = _appKit.subscribeState((state) => {
      if (resolved) return;

      // Connection confirmed
      if (state.isConnected && state.address) {
        resolved = true;
        try { unsub(); } catch (_) {}

        // Get the EIP-1193 provider from the ethers adapter.
        // wallet.js wraps this with `new ethers.BrowserProvider(eip1193)`.
        try {
          const provider = _ethersAdapter.getProvider
            ? _ethersAdapter.getProvider()
            : _appKit.getProvider
              ? _appKit.getProvider()
              : null;

          if (provider && typeof provider.request === 'function') {
            console.log('[mw-appkit] Connected:', state.address.slice(0, 10) + '…');
            resolve(provider);
          } else {
            reject(new Error('AppKit connected but provider is unavailable'));
          }
        } catch (e) {
          reject(e);
        }
      }

      // Modal closed without connecting
      if (!state.open && !state.isConnected) {
        resolved = true;
        try { unsub(); } catch (_) {}
        reject(Object.assign(new Error('User closed the wallet selection modal'), { code: 'MODAL_CLOSED' }));
      }
    });

    // Open the wallet selection modal.
    // AppKit shows: wallet list → QR code → connecting → connected.
    try {
      _appKit.open();
    } catch (e) {
      resolved = true;
      try { unsub(); } catch (_) {}
      reject(e);
    }

    // Safety timeout: 3 minutes for the user to scan/approve
    setTimeout(() => {
      if (!resolved) {
        resolved = true;
        try { unsub(); } catch (_) {}
        reject(new Error('WalletConnect pairing timed out — please try again'));
      }
    }, 180000);
  });
}

/* ── Disconnect ── */
function disconnect() {
  if (!_ready || !_appKit) return;
  try {
    _appKit.disconnect();
    console.log('[mw-appkit] Disconnected');
  } catch (e) {
    console.warn('[mw-appkit] Disconnect error:', e);
  }
}

/* ── State change callback — called on every AppKit state transition ── */
function onStateChange(cb) {
  if (!_ready || !_appKit || typeof cb !== 'function') return function () {};
  return _appKit.subscribeState(cb);
}

/* ── Assemble and expose the bridge ── */
const readyPromise = init();

window.__MW_APPKIT__ = {
  ready: readyPromise,
  connect: connect,
  disconnect: disconnect,
  onStateChange: onStateChange,
};

})();
