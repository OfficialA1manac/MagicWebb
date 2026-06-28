/* ── Reown AppKit bridge (self-hosted — no CDN dependency).
 * Previously loaded from esm.sh CDN (Phase 3 V7.1 fix).
 * Bundled by Vite during Astro build; npm imports resolve to node_modules.
 *
 * Exposes: window.__MW_APPKIT__ = { ready, connect, disconnect, onStateChange }
 *   ready          — Promise<boolean> resolves when init completes (true=ok, false=failed)
 *   connect()      — Promise<EIP1193Provider> opens modal, resolves on connect
 *   disconnect()   — tears down the WC session
 *   onStateChange  — callback(state) called on every state transition
 */

import { createAppKit } from '@reown/appkit';
import { EthersAdapter } from '@reown/appkit-adapter-ethers';

(function () {
'use strict';

let _appKit = null;
let _ethersAdapter = null;
let _ready = false;
let _initError = null;

const projectId = (window.MW_WC_PROJECT_ID || '').trim();
if (!projectId) {
  console.warn('[mw-appkit] No WC project ID — AppKit bridge disabled.');
  window.__MW_APPKIT__ = {
    ready: Promise.resolve(false),
    connect: function () { return Promise.reject(new Error('AppKit not configured (no WC project ID)')); },
    disconnect: function () {},
    onStateChange: function () { return function () {}; },
  };
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

const chains = [primaryChain];

/* ── Init AppKit ── */
async function init() {
  try {
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
    flushPendingCallbacks();
    console.log('[mw-appkit] Reown AppKit bridge initialised (self-hosted, ethers adapter)');
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

      if (state.isConnected && state.address) {
        resolved = true;
        try { unsub(); } catch (_) {}

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

      if (!state.open && !state.isConnected) {
        resolved = true;
        try { unsub(); } catch (_) {}
        reject(Object.assign(new Error('User closed the wallet selection modal'), { code: 'MODAL_CLOSED' }));
      }
    });

    try {
      _appKit.open();
    } catch (e) {
      resolved = true;
      try { unsub(); } catch (_) {}
      reject(e);
    }

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

/* ── State change callback ── */
var _pendingCallbacks = [];
var _onStateUnsub = null;

function onStateChange(cb) {
  if (typeof cb !== 'function') return function () {};
  if (!_ready || !_appKit) {
    // Not ready yet — queue the callback to be flushed once init completes.
    _pendingCallbacks.push(cb);
    return function () {
      var idx = _pendingCallbacks.indexOf(cb);
      if (idx >= 0) _pendingCallbacks.splice(idx, 1);
    };
  }
  return _appKit.subscribeState(cb);
}

// Flush any callbacks that were registered before init() completed.
// Called from the ready promise resolution path.
function flushPendingCallbacks() {
  if (!_ready || !_appKit || _pendingCallbacks.length === 0) return;
  var cbs = _pendingCallbacks.slice();
  _pendingCallbacks = [];
  _onStateUnsub = _appKit.subscribeState(function (state) {
    cbs.forEach(function (cb) { try { cb(state); } catch (_) {} });
  });
}

/* ── Assemble and expose the bridge ── */
const readyPromise = init();

// Only expose the bridge when init succeeds. On failure, __MW_APPKIT__
// stays undefined so wallet.js falls back to the self-hosted WC bundle.
readyPromise.then(function(success) {
  if (success) {
    window.__MW_APPKIT__ = {
      ready: readyPromise,
      connect: connect,
      disconnect: disconnect,
      onStateChange: onStateChange,
    };
  }
});

})();
