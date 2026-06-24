/* ─────────────────────────────────────────────────────────────────────────────
 * MagicWebb — Alpine.js wallet + ethers v6 contract interactions.
 * Chain: Flare Coston2 (chainId 114 / 0x72). Seller-pays 1.5% fee.
 *
 *   ETHERS + ALPINE PROXY (read this first time you touch ethers here):
 *   ──────────────────────────────────────────────────────────────────────
 *   Alpine v3 wraps nested objects on the reactive store with a Proxy and
 *   re-wraps them on every assignment. Ethers v6 uses class-internal private
 *   slots (#pin, #runner, …) to enforce `instanceof AbstractSigner` and
 *   `instanceof AbstractProvider`. A Proxied signer therefore fails Ethers'
 *   runner check with: "Receiver must be an instance of class
 *   AbstractProvider". Three compensating patterns are in force everywhere:
 *
 *     1) Ethers objects live under wallet._raw and are NEVER touched through
 *        the public getters except after Alpine.raw() unwrap.
 *     2) R(obj) calls Alpine.raw(obj) defensively before passing into Ethers.
 *     3) resolveSigner / resolveProvider iterate over multiple representations
 *        of the same EIP-1193 bridge, trying each in order — including a
 *        freshly constructed BrowserProvider rebuilt from the underlying
 *        EIP-1193 (last resort against MetaMask lock / network switch /
 *        tab-sleep stale-signer races).
 *     4) buy/offer/auction etc. eagerly re-acquire the signer via
 *        await provider.getSigner() (Ethers returns a fresh instance per
 *        call) inside ensureSigner — older signed objects are discarded.
 *     5) The action mutex `_busy` prevents two simultaneous confirmations
 *        (a double-click could double-buy); the modal itself enters a
 *        step>=1 state and ignores re-entry into confirm().
 *
 *   All of this is the cumulative answer to the reported error:
 *     "Receiver must be an instance of class AbstractProvider" appearing
 *     alongside the buy-disclaimer toast.
 * ───────────────────────────────────────────────────────────────────────────── */
(function () {
'use strict';

const CHAIN_ID  = Number(window.MW_NETWORK_ID || 114);
const RPC_URL   = window.MW_RPC_URL  || 'https://coston2-api.flare.network/ext/C/rpc';
const EXPLORER  = window.MW_EXPLORER || 'https://coston2-explorer.flare.network';

/*
 * ── WalletConnect overlay protocol (v6 — positive command) ─────────────
 * Replaces the prior flag-gated passive listener pattern (which leaked
 * state across auto-reconnect — see commit history for the
 * "popup-on-boot" bug class). New contract:
 *
 *   • Auto-reconnect at the bottom of this file runs `connect(kind,
 *     {silent:true})`. The silent path emits ZERO overlay events and
 *     does NOT touch any open flag. If the cached pairing session
 *     is still valid, connect() resolves directly. If it expired,
 *     wc.connect() rejects; we surface the error and the user can
 *     click the navbar Scan-QR chip (or Connect Wallet → WalletConnect)
 *     to start a fresh pairing.
 *
 *   • User-initiated `connect('walletconnect')` (silent default false)
 *     dispatches exactly TWO events:
 *       - `mw-wc-show { loading: true }` — overlay opens with spinner.
 *       - `mw-wc-show { uri }`              — first display_uri arrives;
 *                                            overlay renders the QR.
 *
 *   • Either path also listens for `mw-wc-hide` (the navbar chip's
 *     "force-close" / Esc / X / Got it funnel) so the chip can clear
 *     a stale modal.
 *
 * The OLD `mw-wc-uri` and `mw-wc-connecting` events are NOT used by the
 * overlay anymore — we keep them as no-op alias dispatches so any
 * third-party embed or future debugger panel that greps for them
 * doesn't go dark, but the overlay state is driven exclusively by the
 * `mw-wc-show` / `mw-wc-hide` pair.
 */

/* ── Contract addresses: server-injected from .env. NEVER hardcode. ── */
const MARKETPLACE = window.MW_MARKETPLACE || '';
const AUCTION     = window.MW_AUCTION     || '';
const OFFERBOOK   = window.MW_OFFERBOOK   || '';
if (!MARKETPLACE || !AUCTION || !OFFERBOOK) {
  console.error('MagicWebb: contract addresses missing from server inject — wallet disabled.');
}

/* ── 1.5% seller-pays fee ── */
const FEE_BPS = 150n;
const feeOf    = (wei) => (BigInt(wei) * FEE_BPS) / 10000n;
const netOfFee = (wei) => BigInt(wei) - feeOf(wei);

/* ── WalletConnect project id (server-injected) ── */
const WC_PROJECT_ID = (window.MW_WC_PROJECT_ID || '').trim();

/* ── Hoisted constant: fallback opts for `modals.open()` when called with
 * undefined / null / non-object. Reused on every malformed dispatch so
 * the happy path doesn't allocate a new object. Frozen so a caller
 * (defensive only) can't mutate it across invocations.
 * ── */
const MODAL_OPTS_FALLBACK = Object.freeze({
  userInitiated: true,
  kind: 'info',
  icon: '\u2139',
  title: 'This action isn\u2019t available right now',
  subtitle: '',
  disclaimer: 'Please try again from a button on the page.',
  ctaLabel: 'Dismiss',
  run: async ({ fail }) => {
    fail({ title: 'Action unavailable', body: 'Please retry from a button on the page.' });
  },
});

/* ── AuctionHouse constants ── */
const EXTENSION_WINDOW = 180;

/* ─────────────────────────────────────────────────────────────────────────────
 * Proxy unwrap helpers — EIGENVALUE FOR THE "AbstractProvider" BUG.
 * ───────────────────────────────────────────────────────────────────────────── */
function R(obj) {
  if (obj == null) return obj;
  if (typeof Alpine !== 'undefined' && typeof Alpine.raw === 'function') {
    try { return Alpine.raw(obj); } catch (_) {}
  }
  return obj;
}

/**
 * Resolve a usable AbstractSigner from the wallet store.
 *
 * Strategy:
 *   1. Take `_raw.signer` after Alpine.raw() unwrap (fast path).
 *   2. If absent/stale, ask `_raw.provider` for a fresh signer.
 *   3. If still unresolved, walk EIP-1193 sources from `_raw.wc` or
 *      `window.ethereum` to construct a fresh BrowserProvider and
 *      get a signer that way — defeats MetaMask "lock" pop and tab-sleep
 *      stale-signer races.
 *
 * Returns the raw signer (NOT wrapped in Proxy) or null.
 */
async function resolveSigner(store) {
  // Path 1
  try {
    const s = R(store?._raw?.signer);
    if (s && typeof s.signTransaction === 'function' && typeof s.getAddress === 'function') {
      return s;
    }
  } catch (_) {}

  // Path 2 — re-acquire via provider
  for (const prov of [R(store?._raw?.provider), store?._raw?.provider].filter(Boolean)) {
    try {
      const s = R(await prov.getSigner());
      if (s && typeof s.signTransaction === 'function') {
        store._raw.signer = R(s);
        return s;
      }
    } catch (_) {}
  }

  // Path 3 — full EIP-1193 reconstruction (last-resort).
  // v23.2 — WalletConnect-only. window.ethereum / any window-injected
  // provider is no longer part of the connect surface, so the only
  // valid EIP-1193 source for signing is the cached WC session on the
  // wallet as `store._raw.wc`. Returning null on miss is the
  // consistent failure mode: the caller surfaces "Connect wallet first"
  // and the user re-pairs via QR.
  let eip1193 = null;
  try { eip1193 = R(store?._raw?.wc) || store?._raw?.wc || null; } catch (_) {}
  if (!eip1193 || typeof eip1193.request !== 'function') return null;
  try {
    const fresh = new ethers.BrowserProvider(eip1193);
    const s = R(await fresh.getSigner());
    if (s) {
      store._raw.provider = R(fresh);
      store._raw.signer   = s;
      return s;
    }
  } catch (_) {}
  return null;
}

/**
 * Resolve a usable AbstractProvider — used for view-calls (free, no signing).
 * Same multi-strategy pattern as resolveSigner. Returns raw provider or null.
 */
async function resolveProvider(store) {
  for (const p of [R(store?._raw?.provider), store?._raw?.provider].filter(Boolean)) {
    if (p && typeof p.getNetwork === 'function' && typeof p.getBlockNumber === 'function') return p;
  }
  // Fallback: WC provider only.
  // v23.2 — WalletConnect-only; window.ethereum has been removed from
  // the connect surface so the resolveProvider fallback must NOT
  // resurrect it (otherwise a stale WC session could end up reading
  // prices through an unrequested injected provider).
  let eip = R(store?._raw?.wc) || store?._raw?.wc || null;
  if (!eip || typeof eip.request !== 'function') return null;
  try {
    const fresh = new ethers.BrowserProvider(eip);
    store._raw.provider = R(fresh);
    return fresh;
  } catch (_) {
    return null;
  }
}

/* ─────────────────────────────────────────────────────────────────────────────
 * ABIs — minimal, mirror the seller-pays contracts.
 * ───────────────────────────────────────────────────────────────────────────── */
const MARKETPLACE_ABI = [
  'function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external',
  'function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external',
  'function cancel(address coll, uint256 id) external',
  'function buy(address coll, uint256 id, address seller) external payable',
];
const AUCTION_ABI = [
  'function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat) external returns (uint256)',
  'function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat) external returns (uint256)',
  'function bid(uint256 id) external payable',
  'function settle(uint256 id) external',
  'function cancelEarly(uint256 id) external',
  'function withdrawRefund() external',
  'function pendingReturns(address) external view returns (uint256)',
];
const OFFERBOOK_ABI = [
  'function makeOffer(address coll, uint256 tokenId, uint128 principal, uint64 expiresAt) external payable',
  'function makeOffer1155(address coll, uint256 tokenId, uint128 principal, uint128 units, uint64 expiresAt) external payable',
  'function acceptOffer(address coll, uint256 tokenId, address bidder) external',
  'function rejectOffer(address coll, uint256 tokenId, address bidder) external',
  'function refundExpiredOffer(address coll, uint256 tokenId, address bidder) external',
  'function positions(address, uint256, address) external view returns (uint128 principal, uint128 units, uint64 expiresAt, uint8 standard)',
];
const ERC721_ABI = [
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, uint256 op) external view returns (bool)',
  'function ownerOf(uint256 tokenId) external view returns (address)',
  'function balanceOf(address owner) external view returns (uint256)',
];
const ERC1155_ABI = [
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
  'function balanceOf(address account, uint256 id) external view returns (uint256)',
];

/* ─────────────────────────────────────────────────────────────────────────────
 * Plain-English revert mapping
 * ───────────────────────────────────────────────────────────────────────────── */
function revertMessage(e) {
  if (e?.code === 4001 || e?.code === 'ACTION_REJECTED') return 'You rejected the request.';
  const raw = [e?.reason, e?.shortMessage, e?.info?.error?.message, e?.data?.message, e?.message].filter(Boolean).join(' ');
  const map = [
    ['WrongValue',      "Amount sent must equal the offer amount exactly."],
    ['WrongBidValue',   "Amount sent must equal the bid amount exactly."],
    ['WrongPrice',      "Amount sent must equal the listing price exactly."],
    ['BelowMinPrice',   'Minimum is 0.01 FLR.'],
    ['BidTooLow',       'Your bid is below the minimum increment.'],
    ['NotApproved',     'Approve the contract to manage this NFT first.'],
    ['NotOwner',        "You don't hold this NFT."],
    ['NotSeller',       'Only the seller can do that.'],
    ['Expired',         'This listing or offer has expired.'],
    ['InvalidExpiry',   'Pick an expiry within the allowed window.'],
    ['AuctionEnded',    'This auction has already ended.'],
    ['AuctionLive',     'This auction is still live.'],
    ['OfferActive',     "This offer hasn't expired yet."],
    ['NoOffer',         'No active offer found.'],
    ['InvalidWindow',   'Duration is outside the allowed range.'],
    ['EntriesHalted',   'The marketplace is temporarily paused — settlements and refunds still work.'],
    ['NotActive',       'This auction is not active.'],
    ['NotSettled',      'This auction has not settled yet.'],
    ['BidOverflow',     'Bid total exceeds the supported maximum.'],
    ['NothingToWithdraw','No pending refund to withdraw.'],
    ['insufficient funds','Not enough FLR to cover the amount plus gas.'],
    ['user rejected',   'You rejected the request.'],
    ['missing revert data','Transaction reverted on-chain. The item may have just sold or changed — refresh and retry.'],
  ];
  const lower = (raw || '').toLowerCase();
  for (const [needle, msg] of map) {
    if (lower.includes(needle.toLowerCase())) return msg;
  }
  if (/receiver must be an instance of class abstractprovider|runner provider|runner must be/i.test(raw)) {
    return 'Wallet connection lost — please reconnect and try again.';
  }
  if (/revert|call_exception/i.test(raw)) {
    return 'Transaction reverted — the item may have just sold or changed. Refresh and retry.';
  }
  return raw || 'Transaction failed.';
}

/* ─────────────────────────────────────────────────────────────────────────────
 * Formatting helpers
 * ───────────────────────────────────────────────────────────────────────────── */
function fmtFLR(wei, decimals = 4) {
  if (!wei || wei === '0') return (0).toFixed(decimals);
  try {
    const bi = BigInt(wei);
    const flr = Number(bi) / 1e18;
    return flr.toFixed(decimals);
  } catch (_) { return wei; }
}
function fmtAddr(a) {
  if (!a) return '';
  if (a.length < 10) return a;
  return a.slice(0, 6) + '…' + a.slice(-4);
}

/* ─────────────────────────────────────────────────────────────────────────────
 * Alpine init — registers the modals + wallet Alpine stores.
 * ───────────────────────────────────────────────────────────────────────────── */
window.addEventListener('alpine:init', () => {

  // ── modals store: drives the global action_modal partial ──
  Alpine.store('modals', {
    open: false,
    actionKind: 'buy',
    icon: '⚐',
    title: '', subtitle: '',
    ctaLabel: '',
    summary: [],
    disclaimer: '',
    step: 0,          // 0 pre-confirm, 1 wallet-signing, 2 confirming, 3 done, 4 error
    stepLabel: '',
    success: false,
    successTitle: '', successBody: '',
    errorTitle: '', errorBody: '',
    txHash: '',
    progress: 0,      // 0..100 — drives the top sliding bar
    // _resolver is the per-open callback dispatched on click of Confirm.
    _resolver: null,

    /**
     * Open the modal with the supplied summary. Promise resolves on:
     *   { ok: true,  txHash }  — confirmed on-chain
     *   { ok: false, error }   — errored or user-cancelled
     *   null                   — busy with prior modal (auto-skipped)
     *
     * Defensive contract: if a caller passes undefined / null / a primitive
     * (rather than a real opts object), fall back to MODAL_OPTS_FALLBACK
     * (a friendly dismissable info-card) instead of throwing on
     * `opts.kind`. The action_modal partial's `x-on:open-action.window`
     * listener forwards `$event.detail` directly into modals.open(), so a
     * third-party or stale dispatch that fires with no detail would
     * otherwise pin Alpine with "Cannot read properties of undefined
     * (reading 'kind')" globally.
     *
     * Implementation note: the sanitiser is INLINED here rather than
     * factored into a `_sanitizeOpts()` method. Alpine.store objects are
     * reactive Proxies; methods defined on the source literal are not
     * always returned as callable functions by the Proxy.get trap
     * (depends on interceptor chain). Inlining removes the
     * `_sanitizeOpts is not a function` regression risk entirely.
     */
    open(opts) {
      opts = (opts && typeof opts === 'object') ? opts : MODAL_OPTS_FALLBACK;
      // v23.1 — user-initiated gate. The action modal must NEVER auto-show
      // because of a stray open-action event, a misbehaving extension, or
      // any future code path that forgot to pass an opts object. Any
      // caller that wants the modal to actually appear MUST pass
      // userInitiated:true. We log the blocked attempt with a stack
      // trace so the offender surfaces in the dev console even on
      // production deploys, then return a resolved null without
      // flipping this.open = true. Belt-and-braces: also keep the
      // existing busy-guard loop (eight-second max wait, then null)
      // untouched — it still serves the legitimate concurrent-modal
      // debounce.
      if (opts.userInitiated !== true) {
          try {
            const e = new Error('modals.open() blocked — missing opts.userInitiated=true');
            console.warn('[mw] action modal auto-open blocked:', e.message, (e.stack || '').split('\n').slice(1, 4).join(' | '));
          } catch (_) {
            console.warn('[mw] action modal auto-open blocked: opts=', JSON.stringify(opts));
          }
          return Promise.resolve(null);
        }
      if (this.open && this._resolver) {
        return new Promise((resolve) => {
          const tick = setInterval(() => {
            if (!this.open) {
              clearInterval(tick);
              resolve(this.open(opts));
            }
          }, 200);
          setTimeout(() => { clearInterval(tick); resolve(null); }, 8000);
        });
      }
      return new Promise((resolve) => {
        this.actionKind = opts.kind || 'buy';
        this.icon       = opts.icon || '⚐';
        this.title      = opts.title || '';
        this.subtitle   = opts.subtitle || '';
        this.ctaLabel   = opts.ctaLabel || 'Continue';
        this.summary    = opts.summary || [];
        this.disclaimer = opts.disclaimer || '';
        this.step = 0;
        this.stepLabel = '';
        this.progress = 0;
        this.success = false;
        this.txHash = '';
        this.successTitle = ''; this.successBody = '';
        this.errorTitle = '';   this.errorBody = '';
        const resolver = async () => {
          try {
            await opts.run({
              setStep: (n, label) => {
                this.step = n; this.stepLabel = label || '';
                this.progress = Math.max(this.progress, n === 1 ? 35 : n === 2 ? 75 : n >= 3 ? 100 : 0);
              },
              setProgress: (p) => { this.progress = Math.max(this.progress, p); },
              done: (detail = {}) => {
                this.step = 3;
                this.progress = 100;
                this.success = true;
                this.successTitle = detail.title || 'Done';
                this.successBody = detail.body || '';
                this.txHash = detail.txHash || '';
                window.dispatchEvent(new CustomEvent('mw-modal-done', {
                  detail: { action: this.actionKind, txHash: this.txHash },
                }));
                resolve({ ok: true, txHash: detail.txHash });
                setTimeout(() => { if (this.open && this._resolver === resolver) this.dismiss(); }, 9000);
              },
              fail: (e) => {
                this.step = 4;
                this.success = false;
                const title = e?.title || 'Failed';
                const body  = revertMessage(e);
                this.errorTitle = title;
                this.errorBody = body;
                window.dispatchEvent(new CustomEvent('mw-modal-failed', {
                  detail: { action: this.actionKind, error: body },
                }));
                resolve({ ok: false, error: body });
              },
            });
          } catch (e) {
            this.step = 4;
            this.success = false;
            this.errorTitle = 'Failed';
            this.errorBody = revertMessage(e);
            window.dispatchEvent(new CustomEvent('mw-modal-failed', {
              detail: { action: this.actionKind, error: this.errorBody },
            }));
            resolve({ ok: false, error: this.errorBody });
          }
        };
        this._resolver = resolver;
        this.open = true;
      });
    },
    confirm() {
      if (this.step >= 1) return; // re-entry guard (atomic action)
      const r = this._resolver;
      if (!r) return;
      this.step = 1;
      this.stepLabel = 'Confirm in your wallet…';
      this.progress = 25;
      setTimeout(() => { Promise.resolve().then(r).catch((e) => toast(revertMessage(e), 'error')); }, 30);
    },
    dismiss() {
      if (this.step === 1 || this.step === 2) {
        toast('Action cancelled — your wallet may still show a pending prompt.', 'info');
      }
      this.open = false;
      this._resolver = null;
      this.progress = 0;
    },
  });

  // ── wallet store ──
  Alpine.store('wallet', {
    _raw: { provider: null, signer: null, wc: null },
    address: null,
    chainId: null,
    jwt:     localStorage.getItem('mw_jwt') || null,
    unread:  0,
    busy:    false,
    state:   'idle', // 'idle' | 'connecting' | 'connected' | 'awaiting' | 'error'
    // ── "Saved wallet" hydration (v13 — REPLACES auto-reconnect) ──
    // On prior deploys we auto-connected on every page load when a
    // `localStorage.mw_addr` was present. That violated user consent and
    // was the source of the recurring "Tries to connect to my MetaMask
    // wallet automatically" complaint. The new behaviour:
    //
    //   • The hydration block at the bottom of this file sets
    //     `savedAddress` / `savedKind` ONLY (no connect() call). The
    //     UI surfaces a "Saved wallet 0x1234…abcd — [Reconnect] [×]"
    //     pill in the navbar when `savedAddress` is set and the user
    //     is not currently connected.
    //   • The user clicks Reconnect to re-establish the session with
    //     their previous wallet, or × to forget the saved entry and
    //     start fresh.
    //   • On successful reconnect, savedAddress is cleared (so the
    //     pill disappears once the session is live).
    savedAddress: null, // previously-connected wallet (from localStorage)
    savedKind:    null, // 'injected' | 'walletconnect'

    get provider() { return this._raw.provider; },
    get signer()   { return this._raw.signer;   },
    set provider(v) { this._raw.provider = R(v); },
    set signer(v)   { this._raw.signer   = R(v); },

    get shortAddr() {
      return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : '';
    },
    get shortSavedAddr() {
      return this.savedAddress ? this.savedAddress.slice(0, 6) + '…' + this.savedAddress.slice(-4) : '';
    },
    get hasSavedWallet() { return !!this.savedAddress; },
    get connected()      { return !!this.address && this.state === 'connected'; },
    get isWalletConnect(){ return (localStorage.getItem('mw_kind') || '') === 'walletconnect'; },
    get stateError()     { return this._stateError || null; },

    setState(s, opts = {}) {
      this.state = s;
      this._stateError = opts.error || null;
      window.dispatchEvent(new CustomEvent('mw-wallet-state', {
        detail: { state: s, error: opts.error },
      }));
    },

    // Re-establish a session with the previously-saved wallet. Resolves
    // the savedAddress→address boundary so the pill collapses to the
    // connected pill on success. Error surfaced via toast + state event
    // (the saved pill stays visible so the user can try a different
    // wallet or retry without losing context).
    async reconnectSaved() {
      if (!this.savedAddress) return;
      const kind = this.savedKind || 'injected';
      await this.connect(kind, { silent: false });
      if (this.connected && this.address === this.savedAddress) {
        // Reconnect succeeded — clear the pill.
        this.savedAddress = null;
      }
    },

    // User dismissed the saved wallet — forget it so the pill collapses.
    // Does not affect a live session (the connect() flow manages its own
    // state). Preserves the JWT for any concurrent read-only API calls
    // that may still rely on it (notifications bell, profile view).
    forgetSaved() {
      this.savedAddress = null;
      this.savedKind    = null;
      try {
        localStorage.removeItem('mw_addr');
        localStorage.removeItem('mw_kind');
      } catch (_) {}
    },

    // ── Connect (injected wallet OR WalletConnect v2 via QR) ──

    // v23.2 — WalletConnect-only. The old `connect(kind, opts)` API
    // branched on `kind === 'walletconnect'` vs `'injected'`; with the
    // MetaMask/browser-injected path removed entirely per user request,
    // the function takes only an opts bag with `{ silent }`. Every
    // caller — the navbar Connect Wallet button, the nft_picker.html
    // connect gate, the saved-wallet pill's reconnectSaved(), and the
    // mobile-drawer button — funnels through this single entry point.
    async connect({ silent = false } = {}) {
      // Belt-and-braces: silent reconnect (page-boot auto-reconnect for
      // returning users with a cached address in localStorage) may still
      // be in-flight when the user explicitly clicks the navbar/picker
      // "Connect Wallet" button. Only short-circuit SILENT reconnects;
      // always honor user intent (regardless of current state) so they
      // cannot get stranded.
      if (this.state === 'connecting' && silent) return;
      // Rapid double-click debounce. 1.5s window — short enough that a
      // real retry after a rejected WC prompt is unaffected, long enough
      // to absorb a jittery double-tap.
      if (!silent) {
        const now = Date.now();
        if (now - (this._connectStartedAt || 0) < 1500) {
          return;
        }
        this._connectStartedAt = now;
      }
      const wasError = this.state === 'error';
      this.setState('connecting');
      try {
        if (!WC_PROJECT_ID) throw new Error('WalletConnect is not configured on this server.');
        const eip1193 = await this._wcConnect({ silent });
        const provider = new ethers.BrowserProvider(eip1193);
        const accounts = await provider.send('eth_requestAccounts', []);
        if (!accounts?.length) throw new Error('No account authorized.');
        const network = await provider.getNetwork();
        if (Number(network.chainId) !== CHAIN_ID) {
          // WC pairs on the network the user's wallet is currently
          // switched to. If the wallet is on the wrong chain we cannot
          // auto-switch from the dApp side (no EIP-3326 surface) — the
          // user must re-pair after flipping their wallet's network.
          // Surface a single explicit toast and bail; do NOT silently
          // mark connected, otherwise every ethers-provider call would
          // target the wrong chain and instant-revert at the contract.
          if (!silent) {
            toast(`Connected wallet is on chain #${Number(network.chainId)} — expected Coston2 (114). Switch networks in your wallet, then Re-pair via QR.`, 'error', 8000);
          }
          this.setState('error');
          this._connectStartedAt = 0; // release double-click debounce
          return;
        }
        // Always store ROOT ethers objects unwrapped. Setters nested-call
        // R() so double-wrap is impossible.
        this._raw.provider = R(provider);
        this._raw.signer   = R(await provider.getSigner());
        this.address       = accounts[0].toLowerCase();
        this.chainId       = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        // Hardcode the saved kind — there is only one connect method.
        // Older clients that saved `"injected"` will overwrite this on
        // next successful connect. Reading the value back is still
        // supported for the saved-wallet pill so the WC QR-reconnect
        // button label can branch on history.
        localStorage.setItem('mw_kind', 'walletconnect');

        // Named handler refs so ANY prior registration on the same eip1193
        // (WC session object is unique per pairing) can be removed before
        // re-mounting on the next session. The pre-WC-only listener-stack
        // bug (window.ethereum singleton, repeated connect/disconnect
        // cycles) is gone; this defensive teardown stays as a belt.
        const _onChain = () => location.reload();
        const _onAccts = (accs) => {
          if (!accs || !accs.length) { this.disconnect(); return; }
          this.address = accs[0].toLowerCase();
          localStorage.setItem('mw_addr', this.address);
          location.reload();
        };
        let _onDisc = null;

        // Tear down any prior pair before re-registering. window.ethereum
        // persists across reconnects — listener stacking was the post-fix
        // regression caught by code-review.
        if (this._eipHandlers && eip1193?.removeListener) {
          try { eip1193.removeListener('chainChanged',    this._eipHandlers.chain); } catch (e) {}
          try { eip1193.removeListener('accountsChanged', this._eipHandlers.accts); } catch (e) {}
          if (this._eipHandlers.disc) {
            try { eip1193.removeListener('disconnect',     this._eipHandlers.disc); } catch (e) {}
          }
        }
        this._eipHandlers = { chain: _onChain, accts: _onAccts };

        // EIP-1193 'disconnect' fires on WalletConnect session endings only.
        // Injected providers don't surface this event for user-initiated UI
        // changes — the disconnect button there calls this.disconnect()
        // directly. We do not skip the WC registration on subsequent
        // connect() cycles: each new connect() re-binds to a fresh WC
        // SignClient instance anyway, and the eip1193.removeListener call
        // above clears any prior pair defensively.
        if (kind === 'walletconnect' && eip1193?.on) {
          _onDisc = () => this.disconnect();
          this._eipHandlers.disc = _onDisc;
          eip1193.on('disconnect', _onDisc);
        }

        // chainChanged + accountsChanged are standard EIP-1193 events from
        // every compliant provider. The previous version gated these on
        // kind==='walletconnect' which left injected (MetaMask, Rabby,
        // mobile wallets) blind to mid-session switches — this was the
        // audit's #1 critical. ethers.BrowserProvider caches `.provider.network`
        // at construction, so an unnoticed chainChanged routes the next tx
        // against the stale snapshot and instant-reverts on the wrong chain.
        // accountsChanged on injected reloads because the cached Signer is
        // bound to the prior address; on WC, SignerClient reflows account-
        // keyed sessions automatically.
        if (eip1193?.on) {
          eip1193.on('chainChanged', _onChain);
          eip1193.on('accountsChanged', _onAccts);
        }

        await this._authenticate();
        await this.refreshUnread();
        this.setState('connected');
        if (!silent) toast(kind === 'walletconnect'
          ? 'Connected via WalletConnect'
          : 'Wallet connected', 'success');
      } catch (e) {
        this.setState('error', { error: e });
        // Clear the double-click debounce so a real retry after a
        // user-clicked "reject" in MetaMask is unblocked. Without this
        // the user would have to wait 1.5s for the debounce window to
        // expire before they could even try clicking again.
        this._connectStartedAt = 0;
        if (wasError || !silent) toast(revertMessage(e), 'error');
      }
    },

    // WalletConnect v2 — owns the entire pairing UX through the
    // partials/wc_qr_overlay.html overlay (we render our own QR matrix
    // via the self-hosted qrcode.min.js encoder). The SDK's built-in
    // modal was disabled because:
    //   (a) it pops up INSTANTLY on init — was the source of the
    //       popup-instantly-show-up complaint (now fixed by the
    //       positive-command protocol below);
    //   (b) it fetches assets from walletconnect.com which is blocked
    //       on some networks / policies, leaving a blank box where the
    //       QR should be — was the no-QR-showing complaint;
    //   (c) its Got it affordance is not tuned to our 5-color palette.
    //
    // Sequencing (v6 positive-command protocol):
    //   1. If !silent, immediately dispatch `mw-wc-show { loading: true }`
    //      so the overlay opens with the spinner BEFORE the WC relay
    //      round-trip completes — defeats the blank-flash race.
    //   2. After init, attach `display_uri` listener and call wc.connect().
    //   3. The SDK emits `display_uri` → our handler caches the URI and
    //      dispatches `mw-wc-show { uri }` so the overlay paints the QR.
    //   4. wc.connect() resolves when the user scans the wallet.
    //
    // Silent path (auto-reconnect on page boot): emits ZERO overlay
    // events. Even if the SDK's display_uri listener fires for a stale
    // session, our handler early-returns on `silent`. The overlay stays
    // closed on page boot for returning users.
    async _wcConnect({ silent = false } = {}) {
      // (1) User-initiated only: open with spinner BEFORE init awaits.
      if (!silent) {
        try {
          window.dispatchEvent(new CustomEvent('mw-wc-show', {
            detail: { loading: true },
          }));
          // No-op alias for any debug-only listener still watching the
          // legacy event name.
          window.dispatchEvent(new CustomEvent('mw-wc-connecting'));
        } catch (_) {}
      }
      // v23 — Try multiple CDNs in sequence (esm.sh ?bundle-deps,
      // ?bundle, jsdelivr). esm.sh periodically changes its bundling
      // shape and that has stranded every user mid-pick before. If
      // EVERY one fails emit a clean, user-actionable error rather
      // than a generic 'failed to load' bubble &mdash; the outer
      // connect() catch surfaces this via the standard toast so the
      // user sees 'WalletConnect is temporarily unavailable. Please
      // use the Browser Wallet option for now.' and picks MetaMask.
      // v23.6 — self-hosted 1.8 MB UMD WalletConnect SDK. The previous
      // (v23.3, v23.4, v23.5) attempts to load the SDK as an ES module
      // from a CDN or self-hosted-served bundle all failed for a single
      // root cause: the published ESM bundles (esm.sh `bundle-deps` and
      // jsdelivr `+esm`) are META bundles whose first line is itself a
      // relative-import ("from '/npm/...'") the browser resolves against
      // the page origin. When the page is served from magicwebb.fly.dev,
      // those relative imports fetch magicwebb.fly.dev/npm/...+esm and
      // return 404. The `+esm` bundle also exhibited a
      // "Maximum call stack size exceeded" RangeError when the chain
      // failed at the relay crypto worker. The solution is the BUILT
      // UMD bundle at `dist/index.umd.min.js` (~1.8 MB, jsdelivr
      // Rollup v2.79.2 + Terser v5.39.0 output) which has ZERO internal
      // relative imports and attaches to a single global namespace:
      //   globalThis["@walletconnect/ethereum-provider"]
      // where:
      //   - `.default` is the `EthereumProvider` class (CommonJS-style
      //     default export)
      //   - `.EthereumProvider` is the same class duplicated as a named
      //     export for native ESM-style consumers
      //
      // We load the UMD via `<script defer>` in layout.html BEFORE
      // wallet.js. Deferred scripts execute in document order, so
      // `window["@walletconnect/ethereum-provider"]` is synchronously
      // populated by the time `alpine:init` fires (during which
      // `wallet.connect({silent:true})` is occasionally called for
      // auto-reconnect of returning users) and well before any user
      // click on Connect Wallet.
      //
      // SECURITY NOTE: the UMD exposes a global under the package name.
      // That global is reachable from any other origins' scripts
      // included via future XSS, but CSP `script-src 'self'` already
      // restricts who can inject — and the wallet object's persist /
      // session methods are only called from inside our own connect()
      // flow, so the attack surface is bounded.
      const _WC_NAMESPACE = window['@walletconnect/ethereum-provider'];
      if (!_WC_NAMESPACE) {
        // Belt-and-braces: the static <script defer> tag in layout.html
        // is what loads this. If the user lands here without that tag
        // having run (rare — only on a network that blocks the script
        // tag itself, which CSP 'self' should prevent) the namespace
        // is undefined. Surface an actionable error rather than the
        // old "WalletConnect is unavailable" that gave no recourse.
        throw new Error(
          'WalletConnect was not loaded. Hard-refresh (Ctrl-Shift-R), disable extensions that block scripts (NoScript/uBlock), or open an incognito window.'
        );
      }
      // Both `default` (UMD default-export) and `EthereumProvider` (named
      // export, present for native ESM consumers) are the same class.
      // Prefer `EthereumProvider` to match the v23.2-v23.5 public API
      // so any code-review grep on the call site is stable.
      const _EthereumProvider = _WC_NAMESPACE.EthereumProvider || _WC_NAMESPACE.default;
      if (!_EthereumProvider) {
        throw new Error('WalletConnect SDK loaded but EthereumProvider class is missing — incompatible bundle version.');
      }
      let wc;
      try {
        wc = await _EthereumProvider.init({
          projectId: WC_PROJECT_ID,
          chains:    [CHAIN_ID],
          rpcMap:    { [CHAIN_ID]: RPC_URL },
          showQrModal: false,
          metadata: {
            name:    'MagicWebb',
            description: 'Non-custodial NFT marketplace on Flare Network',
            url:     window.location.origin,
            icons:   [`${window.location.origin}/static/icon-512.png`],
          },
        });
      } catch (e) {
        // Init can fail because: (a) the user is offline, (b) the
        // walletconnect relay socket can't be opened, (c) the project
        // ID is rejected by api.reown.com's metadata validator. Surface
        // a plausible error so the user can tell the difference between
        // "their network" and "our config".
        throw new Error('WalletConnect init failed: ' + (e?.message || e) +
          ' — try a hard refresh, check status.walletconnect.com, or contact support if it persists.');
      }
      this._raw.wc = R(wc);

      wc.on('display_uri', (uri) => {
        // Positive-command protocol: silent path emits ZERO overlay
        // events; user-initiated path emits `mw-wc-show { uri }` so
        // the overlay can paint the QR.
        if (silent) return;
        if (typeof uri !== 'string' || !uri.startsWith('wc:')) return;
        window.MW_WC_URI = uri;
        try {
          window.dispatchEvent(new CustomEvent('mw-wc-show', { detail: { uri } }));
          // Legacy alias for any downstream listener still keyed on it.
          window.dispatchEvent(new CustomEvent('mw-wc-uri', { detail: uri }));
        } catch (_) {}
      });

      try {
        await wc.connect();
      } catch (e) {
        try { wc.disconnect(); } catch (_) {}
        // Tell the overlay to dismiss so the user isn't stranded on a
        // frozen spinner after a cancelled / failed pairing.
        if (!silent) {
          try { window.dispatchEvent(new CustomEvent('mw-wc-hide')); } catch (_) {}
        }
        throw e;
      }
      return wc;
    },

    disconnect() {
      try { this._raw.wc?.disconnect?.(); } catch (_) {}
      this._raw = { provider: null, signer: null, wc: null };
      this.address = null;
      this.jwt = null;
      this.unread = 0;
      this.setState('idle');
      localStorage.removeItem('mw_addr');
      localStorage.removeItem('mw_jwt');
      localStorage.removeItem('mw_kind');
      // Always tell the overlay to release if it had been open. The
      // overlay's mw-wallet-state listener will ALSO auto-close when
      // state leaves {connecting, connected} but a programmatic dispatch
      // is the belt vs. any future state-event timing regression.
      try { window.dispatchEvent(new CustomEvent('mw-wc-hide')); } catch (_) {}
    },

    async _switchChain() {
      // v23.2 — WalletConnect-only. Chain switching is handled inside
      // the WC session: the wallet negotiates the network per-pairing
      // (the user picks Coston2 in their mobile/hardware wallet when
      // approving the session). Keeping this method as a no-op rather
      // than deleting it because legacy callers in this file may still
      // reference it after a code review — a TypeError here would
      // surface as a connect-failure toast and confuse the user.
    },

    async _authenticate() {
      // SIWE sign-in flow. Every failure path THROWS — replacing the
      // previous console.warn + silent return pattern that left this.jwt=null
      // when connected() proceeded to setState('connected'). That was the
      // source of the "green UI / 401 toasts down the stack" UX bug: a
      // rejected signature looked successful in the navbar but every
      // /api/v1/profile/* + /api/v1/reports call then produced confusing
      // 401 toasts. Now the parent connect()'s catch flips state→'error'
      // and we surface a typed toast here so the user sees what actually
      // went wrong (signature rejection vs. server unavailable).
      try {
        const nonceRes = await fetch('/auth/nonce?address=' + this.address);
        if (!nonceRes.ok) throw new Error('/auth/nonce HTTP ' + nonceRes.status);
        const { nonce } = await nonceRes.json();
        const message = `Sign in to MagicWebb\nAddress: ${this.address}\nNonce: ${nonce}`;
        const sig = await R(this.signer).signMessage(message);
        const verifyRes = await fetch('/auth/verify', {
          method:  'POST',
          headers: { 'Content-Type': 'application/json' },
          body:    JSON.stringify({ address: this.address, message, signature: sig }),
        });
        if (!verifyRes.ok) throw new Error('/auth/verify HTTP ' + verifyRes.status);
        const { token } = await verifyRes.json();
        if (typeof token !== 'string' || !token) throw new Error('/auth/verify returned no token');
        this.jwt = token;
        localStorage.setItem('mw_jwt', token);
      } catch (e) {
        // Clean up the half-authenticated state — drop JWT + persisted token
        // so a subsequent retry starts from nonce-issue. We do NOT surface a
        // local toast here; the parent connect() catch already toasts via the
        // canonical revertMessage() message channel. The previous double-toast
        // (inner toast + parent's) was the F-03 audit finding users reported.
        this.jwt = null;
        try { localStorage.removeItem('mw_jwt'); } catch (e) {}
        throw e;
      }
    },

    authHeaders() {
      return this.jwt
        ? { 'Authorization': 'Bearer ' + this.jwt, 'Content-Type': 'application/json' }
        : { 'Content-Type': 'application/json' };
    },

    // ── Notifications ──

    async refreshUnread() {
      if (!this.jwt) return;
      try {
        const res = await fetch('/api/v1/notifications?limit=1', { headers: this.authHeaders() });
        if (res.ok) this.unread = (await res.json()).unread || 0;
      } catch (_) {}
    },
    async markNotificationsRead() {
      if (!this.jwt) return;
      try {
        await fetch('/api/v1/notifications/read', { method: 'POST', headers: this.authHeaders() });
        this.unread = 0;
      } catch (_) {}
    },

    // ── Approvals ──
    // Belt-and-braces: proxy-safe contract construction with explicit
    // unwrap. The view-call CONTRACT uses the provider only (no signer)
    // so `isApprovedForAll` runs as a free eth_call, not a tx.
    async _approveOperator(collection, operator, standard = 'erc721') {
      const signer   = await resolveSigner(this);
      const provider = await resolveProvider(this);
      if (!signer)   throw Object.assign(new Error('Wallet not connected.'), { title: 'Wallet not connected' });
      if (!provider) throw Object.assign(new Error('Provider unavailable.'), { title: 'Provider unavailable' });

      const abi = (standard === 'erc1155') ? ERC1155_ABI : ERC721_ABI;
      // View read: provider only.
      const readContract = new ethers.Contract(collection, abi, R(provider));
      const approved = await readContract.isApprovedForAll(this.address, operator);
      if (approved) return true;
      // Write: signer only.
      const writeContract = new ethers.Contract(collection, abi, R(signer));
      toast('Approve in your wallet…', 'info');
      const tx = await writeContract.setApprovalForAll(operator, true);
      await tx.wait();
      toast('Approved.', 'success');
      return true;
    },

    // ── ensureSigner — the canonical signer-acquisition path ──

    // Returns `null` on any failure so callers can short-circuit with a
    // clear "Connect your wallet first" message rather than letting Ethers
    // throw its AbstractProvider error mid-flow.
    async ensureSigner() {
      // v23.2 — WalletConnect-only. Page-boot NO LONGER auto-reconnects.
      // The previous design (v9-v22) silently tried `_wcConnect({silent:true})`
      // on page load whenever a saved address was present, which opened
      // the QR overlay without an explicit user click. That was the same
      // class of silent-popup that the v23.1 modal-gate was written to
      // prevent, but the gate was bypassed because the original connect()
      // path had its own silent flow. Now we just bail: returning users
      // pair via the explicit saved-wallet pill (`reconnectSaved()` in
      // the navbar / drawer), which calls non-silent `connect()` and shows
      // the QR on a real click. Fresh visitors click Connect Wallet the
      // first time and stay paired via the standard WC session lifetime.
      if (!this.signer) return null;
      const s = await resolveSigner(this);
      if (s) {
        this._raw.signer = s;
        return s;
      }
      // Last-resort: surface a single explicit toast so the user is never
      // left staring at a disabled button with no feedback. The toast
      // names the actual recovery action so the next click is unambiguous.
      // Note: wording is "could not restore" (not "timed out") — ensureSigner
      // does not have a timeout; it bails on first null. The user's mental
      // model is "previous session lost; re-pair needed", which matches
      // the silent-auto-reconnect removal in v23.2.
      try { toast('Could not restore your saved wallet. Click Connect Wallet to re-pair.', 'error', 6000); } catch (_) {}
      const s2 = await resolveSigner(this);
      if (s2) {
        this._raw.signer = s2;
        return s2;
      }
      return null;
    },

    // ── Per-action runner — single-button modal flow ──

    async runAction(opts) {
      if (this.busy) {
        toast('Another action is already in progress — please wait.', 'info');
        return { ok: false, error: 'busy' };
      }
      this.busy = true;
      try {
        const signer = await this.ensureSigner();
        if (!signer) {
          return await Alpine.store('modals').open({
            userInitiated: true,
            kind: 'list', icon: '⚡',
            title: 'Connect your wallet first',
            subtitle: opts.subtitle || '',
            summary: [{ label: 'Action', value: opts.title || 'Continue' }],
            disclaimer: 'Connection is never custodial — your keys stay in your wallet.',
            ctaLabel: 'Got it',
            run: async ({ fail, setStep }) => {
              setStep(1, 'Open the wallet picker…');
              toast('Click "Connect Wallet" above to choose a wallet.', 'info');
              fail({ title: 'Wallet not connected', body: 'Connect a wallet to continue.' });
            },
          });
        }
        const provider = await resolveProvider(this);
        return await Alpine.store('modals').open({
          userInitiated: true,
          kind: opts.kind,
          icon: opts.icon,
          title: opts.title,
          subtitle: opts.subtitle,
          summary: opts.summary || [],
          disclaimer: opts.disclaimer || '',
          ctaLabel: opts.ctaLabel,
          run: async ({ setStep, done, fail }) => {
            try {
              await opts.fn({
                signer:   R(signer),
                provider: R(provider),
                setStep, done, fail,
              });
            } catch (e) {
              fail(e);
            }
          },
        });
      } finally {
        this.busy = false;
      }
    },

    // ── Marketplace: Buy ──

    async buy(collection, tokenId, seller, priceWei) {
      return await this.runAction({
        kind: 'buy',
        icon: '⚐',
        title: 'Buy now',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'You pay',          value: fmtFLR(priceWei, 4) + ' FLR',  tone: 'sky' },
          { label: 'Seller receives',  value: fmtFLR(netOfFee(priceWei).toString(), 4) + ' FLR (98.5%)', tone: 'gold' },
          { label: 'Platform fee',     value: fmtFLR(feeOf(priceWei).toString(), 4) + ' FLR (1.5%)',    tone: '' },
          { label: 'Token',            value: fmtAddr(collection) + ' #' + tokenId, tone: 'violet' },
        ],
        ctaLabel: `Buy for ${fmtFLR(priceWei, 4)} FLR`,
        disclaimer: 'You pay the listed price. Seller receives 98.5% after the 1.5% platform fee at settlement.',
        run: ({ setStep, done, fail }) => this._executeBuy(collection, tokenId, seller, priceWei, { setStep, done, fail }),
      });
    },

    async _executeBuy(collection, tokenId, seller, priceWei, { setStep, done, fail }) {
      try {
        setStep(1, 'Verifying listing on-chain…');
        // Server-side preflight: is the listing still fillable with THIS
        // seller and price? Cuts the "buy a stale listing" race almost
        // entirely. If the preflight fails we surface a meaningful error
        // instead of letting a stale tx fail in the wallet.
        let pf;
        try {
          const r = await fetch(`/api/v1/listings/${collection}/${tokenId}/preflight?seller=${seller}`);
          pf = r.ok ? await r.json() : null;
        } catch (_) { pf = null; }
        if (!pf) { fail({ title: 'Preflight failed', body: 'Could not reach the marketplace. Refresh and try again.' }); return; }
        if (!pf.ok) {
          fail({ title: 'Listing unavailable', body: 'This listing is no longer fillable (sold, cancelled, or the NFT moved).' });
          return;
        }
        if (pf?.price_wei) priceWei = pf.price_wei;

        setStep(1, 'Awaiting wallet signature…');
        // Acquire RAW signer + provider; pass through R() once more for
        // paranoia. Ethers.AbstractSigner / .AbstractProvider `instanceof`
        // checks use private slots — the `#fields`. Alpine's reactive
        // Proxy does NOT transparently forward private-slot reads; the
        // recv slot check will fail and you see "Receiver must be an
        // instance of class AbstractProvider". Always unwrap.
        const signer   = await this.ensureSigner();
        const provider = await resolveProvider(this);
        if (!signer || !provider) {
          fail({ title: 'Wallet lost', body: 'Reconnect and try again.' });
          return;
        }
        // Soft preflight: `staticCall` runs the buy as a free eth_call
        // and reverts with the EXACT on-chain reason if anything is wrong
        // (revoked approval, expired listing, msg.value mismatch, manager
        // paused, seller transferred NFT out, etc.). It is NOT authoritative
        // — some Coston2 RPCs route eth_call for payable functions through
        // the same relay that fails intermittently, which would falsely
        // block valid transactions. We log the soft result, surfaced as
        // a non-blocking toast for the user so they understand the
        // upcoming real-tx prompt may fail, and ALWAYS proceed to the
        // real transaction: a real on-chain revert will surface in
        // MetaMask with the same custom-error reason the staticCall would
        // have surfaced (NotApproved / WrongPrice / Expired / NotOwner).
        setStep(1, 'Checking purchase is fillable…');
        const writeContract = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        try {
          await writeContract.buy.staticCall(collection, tokenId, seller, { value: BigInt(priceWei) });
        } catch (staticErr) {
          try {
            console.warn('soft preflight failed (proceeding):', revertMessage(staticErr), staticErr);
            // Belt-and-braces: a real revert (NotApproved / WrongPrice /
            // Expired / NotOwner) is likely to surface AGAIN in MetaMask
            // with the same reason. Tell the user now so they have
            // context before signing, but do NOT block: a flaky RPC
            // should not silently kill their flow on a perfectly valid
            // listing. The toast is INFORMAITONAL only, not a fail().
            toast('Preflight flagged: ' + revertMessage(staticErr) + ' \u2014 your wallet will surface the same error if it is a real revert, so you can sign safely.', 'info');
          } catch (_) {}
        }
        const tx = await writeContract.buy(collection, tokenId, seller, { value: BigInt(priceWei) });
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({
          txHash: tx.hash,
          title:  'Purchase confirmed',
          body:   `Seller received ${fmtFLR(netOfFee(priceWei).toString())} FLR (1.5% fee deducted).`,
        });
        window.dispatchEvent(new CustomEvent('mw-bought', {
          detail: { collection, tokenId, tx: tx.hash },
        }));
      } catch (e) {
        fail(e);
      }
    },

    // ── Marketplace: List ── -

    async list(collection, tokenId, priceWei, expiresAt, standard = 'erc721') {
      return await this.runAction({
        kind: 'list', icon: '✦',
        title: 'List for sale',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'You list for',            value: fmtFLR(priceWei) + ' FLR', tone: 'sky' },
          { label: 'You receive on sale',     value: fmtFLR(netOfFee(priceWei).toString()) + ' FLR (98.5%)', tone: 'gold' },
          { label: 'Platform fee (on sale)',  value: fmtFLR(feeOf(priceWei).toString()) + ' FLR (1.5%)', tone: '' },
          { label: 'Expires', value: new Date(expiresAt * 1000).toLocaleString(), tone: 'violet' },
        ],
        ctaLabel: 'List for ' + fmtFLR(priceWei) + ' FLR',
        disclaimer: 'Listing is free. The platform fee is deducted from the seller on sale, not at listing time.',
        run: ({ setStep, done, fail }) => this._executeList(collection, tokenId, priceWei, expiresAt, standard, { setStep, done, fail }),
      });
    },

    async _executeList(collection, tokenId, priceWei, expiresAt, standard, { setStep, done, fail }) {
      try {
        setStep(1, 'Approving marketplace…');
        await this._approveOperator(collection, MARKETPLACE, standard);
        setStep(1, 'Sign listing in wallet…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await contract.list(collection, tokenId, BigInt(priceWei), Math.floor(expiresAt));
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Listed for sale', body: 'Live in ~2s of confirm.' });
        window.dispatchEvent(new CustomEvent('mw-listed', { detail: { collection, tokenId, tx: tx.hash } }));
      } catch (e) { fail(e); }
    },

    async cancel(collection, tokenId) {
      return await this.runAction({
        kind: 'cancel', icon: '×',
        title: 'Cancel listing',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [{ label: 'Action', value: 'Cancel your active listing' }],
        ctaLabel: 'Cancel listing',
        disclaimer: 'Cancel is gas-free. Your NFT immediately stops being purchasable.',
        run: ({ setStep, done, fail }) => this._executeCancel(collection, tokenId, { setStep, done, fail }),
      });
    },

    async _executeCancel(collection, tokenId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm cancellation…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await contract.cancel(collection, tokenId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Listing cancelled', body: 'Your NFT is no longer listed.' });
        window.dispatchEvent(new CustomEvent('mw-listed', { detail: { collection, tokenId, cancelled: true } }));
      } catch (e) { fail(e); }
    },

    // ── Auction: Create ──

    async createAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard = 'erc721') {
      const willExtendHazard = (Number(endsAt) - Date.now()/1000) < EXTENSION_WINDOW;
      return await this.runAction({
        kind: 'auction', icon: '♕',
        title: 'Create auction',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Reserve',          value: fmtFLR(reserveWei || '0') + ' FLR', tone: 'sky' },
          { label: 'Min increment',    value: ((minIncBps || 500) / 100).toFixed(2) + '%'
                                          + (minIncFlatWei && minIncFlatWei !== '0' ? ' + ' + fmtFLR(minIncFlatWei) + ' FLR' : ''),
                                          tone: 'violet' },
          { label: 'Auction ends',     value: new Date(endsAt * 1000).toLocaleString(), tone: 'gold' },
          ...(willExtendHazard ? [{ label: 'Heads up', value: 'Within 3 min of end → anti-snipe extends by +3:00', tone: '' }] : []),
        ],
        ctaLabel: 'Create auction — free',
        disclaimer: 'Auction creation is free. Anti-snipe policy: any bid within 3 min of endAt extends the deadline by 3 minutes.',
        run: ({ setStep, done, fail }) => this._executeCreateAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard, { setStep, done, fail }),
      });
    },

    async _executeCreateAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard, { setStep, done, fail }) {
      try {
        setStep(1, 'Approving auction contract…');
        await this._approveOperator(collection, AUCTION, standard);
        setStep(1, 'Confirm auction creation…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await contract.create(
          collection, tokenId,
          BigInt(reserveWei || '0'),
          Math.floor(endsAt),
          minIncBps || 500,
          BigInt(minIncFlatWei || '0'),
        );
        setStep(2, 'Waiting for confirmation…');
        const rcpt = await tx.wait();
        const auctionId = rcpt?.logs?.[0]?.topics?.[1]
          ? parseInt(rcpt.logs[0].topics[1], 16) : null;
        done({
          txHash: tx.hash,
          title: 'Auction created',
          body: auctionId ? `Auction #${auctionId} is live.` : 'Auction is live.',
        });
        window.dispatchEvent(new CustomEvent('mw-auction-created', {
          detail: { collection, tokenId, auctionId, tx: tx.hash },
        }));
      } catch (e) { fail(e); }
    },

    async bid(auctionId, bidAmountWei, endsAt) {
      const willExtend = endsAt && (Number(endsAt) - Math.floor(Date.now()/1000)) < EXTENSION_WINDOW;
      return await this.runAction({
        kind: 'bid', icon: '♝',
        title: willExtend ? 'Last-minute bid — extends 3 minutes' : 'Place a bid',
        subtitle: `Auction #${auctionId}`,
        summary: [
          { label: 'Bid amount', value: fmtFLR(bidAmountWei) + ' FLR', tone: 'sky' },
          ...(willExtend ? [{ label: 'Anti-snipe', value: '+3:00 (auction extends)', tone: 'violet' }] : []),
          { label: 'Escrow',     value: 'Adds to your cumulative total — free', tone: 'gold' },
          { label: 'Refund',     value: 'Top up to retake lead; pull after settle', tone: '' },
        ],
        ctaLabel: `Bid ${fmtFLR(bidAmountWei)} FLR`,
        disclaimer: 'Bids accumulate. If you are outbid your escrow is preserved until settlement or withdraw.',
        run: ({ setStep, done, fail }) => this._executeBid(auctionId, bidAmountWei, { setStep, done, fail }),
      });
    },

    async _executeBid(auctionId, bidAmountWei, { setStep, done, fail }) {
      try {
        setStep(1, 'Sign bid in wallet…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await contract.bid(auctionId, { value: BigInt(bidAmountWei) });
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Bid placed', body: 'You are now the leading bidder.' });
        window.dispatchEvent(new CustomEvent('mw-bid-placed', { detail: { auctionId, tx: tx.hash } }));
      } catch (e) { fail(e); }
    },

    async settle(auctionId) {
      return await this.runAction({
        kind: 'settle', icon: '⚖',
        title: 'Settle auction',
        subtitle: `Auction #${auctionId}`,
        summary: [{ label: 'Outcome', value: 'NFT to highest bidder, escrow to seller (98.5%)', tone: 'gold' }],
        ctaLabel: 'Settle',
        disclaimer: 'Settlement is permissionless. After it runs, losers can pull their refunds via "Withdraw Refund".',
        run: ({ setStep, done, fail }) => this._executeSettle(auctionId, { setStep, done, fail }),
      });
    },

    async _executeSettle(auctionId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm settlement…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await contract.settle(auctionId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Auction settled', body: 'Funds distributed; losers refunded automatically.' });
        window.dispatchEvent(new CustomEvent('mw-auction-settled', { detail: { auctionId } }));
      } catch (e) { fail(e); }
    },

    async cancelEarly(auctionId) {
      return await this.runAction({
        kind: 'cancel', icon: '✕',
        title: 'Cancel auction early',
        subtitle: `Auction #${auctionId}`,
        summary: [{ label: 'Action', value: 'Bidding stops; bidders refunded; NFT returned' }],
        ctaLabel: 'Cancel auction',
        disclaimer: 'Only the seller can cancel an early auction, and only before any bids have been placed.',
        run: ({ setStep, done, fail }) => this._executeCancelEarly(auctionId, { setStep, done, fail }),
      });
    },

    async _executeCancelEarly(auctionId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm cancellation…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await contract.cancelEarly(auctionId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Auction cancelled', body: 'Bidders refunded; NFT returned.' });
        window.dispatchEvent(new CustomEvent('mw-auction-cancelled', { detail: { auctionId } }));
      } catch (e) { fail(e); }
    },

    async withdrawRefund() {
      return await this.runAction({
        kind: 'settle', icon: '↩',
        title: 'Withdraw refund',
        subtitle: 'Auction escrow refund (push failed)',
        summary: [{ label: 'Action', value: 'Pull pending refund to your wallet' }],
        ctaLabel: 'Withdraw',
        run: ({ setStep, done, fail }) => this._executeWithdrawRefund({ setStep, done, fail }),
      });
    },

    async _executeWithdrawRefund({ setStep, done, fail }) {
      try {
        setStep(1, 'Reading pending refund…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const pending = await contract.pendingReturns(this.address);
        if (pending === 0n) {
          fail({ title: 'Nothing to withdraw', body: 'No pending refund on this address.' });
          return;
        }
        Alpine.store('modals').summary = [
          { label: 'Refund amount', value: fmtFLR(pending.toString()) + ' FLR', tone: 'gold' },
          { label: 'To wallet',     value: fmtAddr(this.address), tone: 'sky' },
        ];
        setStep(1, 'Confirm withdrawal…');
        const tx = await contract.withdrawRefund();
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Refund withdrawn', body: `${fmtFLR(pending.toString())} FLR sent to your wallet.` });
      } catch (e) { fail(e); }
    },

    // ── OfferBook ──

    async makeOffer(collection, tokenId, principalWei, expiresAt) {
      return await this.runAction({
        kind: 'offer', icon: '⚐',
        title: 'Make an offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'You escrow',  value: fmtFLR(principalWei) + ' FLR', tone: 'sky' },
          { label: 'Expires',     value: new Date(expiresAt * 1000).toLocaleString(), tone: 'violet' },
          { label: 'Refundable',  value: 'Fully — until accepted, rejected, or expired', tone: 'gold' },
        ],
        ctaLabel: `Escrow ${fmtFLR(principalWei)} FLR`,
        disclaimer: 'Your escrow is fully refundable until the seller accepts. After expiry it returns automatically.',
        run: ({ setStep, done, fail }) => this._executeMakeOffer(collection, tokenId, principalWei, expiresAt, { setStep, done, fail }),
      });
    },

    async _executeMakeOffer(collection, tokenId, principalWei, expiresAt, { setStep, done, fail }) {
      try {
        setStep(1, 'Sign offer in wallet…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await contract.makeOffer(
          collection, tokenId, BigInt(principalWei), Math.floor(expiresAt),
          { value: BigInt(principalWei) },
        );
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer placed', body: 'Funds are escrowed. Auto-refund at expiry.' });
        window.dispatchEvent(new CustomEvent('mw-offer-made', { detail: { collection, tokenId } }));
      } catch (e) { fail(e); }
    },

    async acceptOffer(collection, tokenId, bidder) {
      return await this.runAction({
        kind: 'accept', icon: '✓',
        title: 'Accept offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Bidder',       value: fmtAddr(bidder), tone: 'sky' },
          { label: 'You receive',  value: fmtFLR('0') + ' FLR (98.5% of offer)', tone: 'gold' },
          { label: 'NFT transfers',value: 'To bidder on confirmation', tone: 'violet' },
        ],
        ctaLabel: 'Accept — get paid',
        disclaimer: 'Accepting pays out your share immediately after tx confirmation.',
        run: ({ setStep, done, fail }) => this._executeAcceptOffer(collection, tokenId, bidder, { setStep, done, fail }),
      });
    },

    async _executeAcceptOffer(collection, tokenId, bidder, { setStep, done, fail }) {
      try {
        setStep(1, 'Approving escrow contract…');
        await this._approveOperator(collection, OFFERBOOK);
        setStep(1, 'Confirm acceptance…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await contract.acceptOffer(collection, tokenId, bidder);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer accepted', body: 'You received 98.5% (1.5% fee deducted). NFT transferred.' });
        window.dispatchEvent(new CustomEvent('mw-offer-accepted', { detail: { collection, tokenId, bidder } }));
      } catch (e) { fail(e); }
    },

    async rejectOffer(collection, tokenId, bidder) {
      return await this.runAction({
        kind: 'reject', icon: '↩',
        title: 'Reject offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Bidder',  value: fmtAddr(bidder), tone: 'sky' },
          { label: 'Outcome', value: 'Bidder is fully refunded', tone: 'gold' },
        ],
        ctaLabel: 'Reject & refund',
        run: ({ setStep, done, fail }) => this._executeRejectOffer(collection, tokenId, bidder, { setStep, done, fail }),
      });
    },

    async _executeRejectOffer(collection, tokenId, bidder, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm rejection…');
        const signer = await this.ensureSigner();
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const contract = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await contract.rejectOffer(collection, tokenId, bidder);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer rejected', body: 'Bidder has been refunded in full.' });
        window.dispatchEvent(new CustomEvent('mw-offer-rejected', { detail: { collection, tokenId, bidder } }));
      } catch (e) { fail(e); }
    },

    // ── Reports / profile ──

    async report(targetType, targetId, reason, detail) {
      try {
        const res = await fetch('/api/v1/reports', {
          method: 'POST', headers: this.authHeaders(),
          body: JSON.stringify({ target_type: targetType, target_id: targetId, reason, detail: detail || '' }),
        });
        if (!res.ok) throw new Error('report failed');
        toast('Report submitted. Thank you.', 'success');
      } catch (_) { toast('Could not submit report.', 'error'); }
    },
    async saveProfile(fields) {
      try {
        const res = await fetch(`/api/v1/profile/${this.address}`, {
          method: 'PUT', headers: this.authHeaders(), body: JSON.stringify(fields),
        });
        if (!res.ok) throw new Error('save failed');
        toast('Profile saved.', 'success');
      } catch (_) { toast('Could not save profile.', 'error'); }
    },

  });

  // ── Hydrate the "saved wallet" hint on page load (v13 — NO auto-reconnect).
  // Previously we auto-connected silently here, which popped MetaMask on
  // every page load — that was the source of the recurring complaint:
  // "Tries to connect to my MetaMask wallet automatically I need that
  // fixed". The contract is now:
  //   • We DO NOT call connect() here under any circumstances.
  //   • We surface savedAddress + savedKind + jwt to the reactive store
  //     so the navbar can render a "Saved wallet 0x1234…abcd [Reconnect] [×]"
  //     pill in DISPLAY state. The user must click Reconnect to actually
  //     re-establish the session.
  //   • The pill collapses to nothing on a clean browser (no localStorage).
  //   • The pill disappears on a successful reconnect (reconnectSaved()
  //     clears savedAddress once the live session matches it).
  // The JWT is read but NEVER trusts the user must still attest via sign-in
  // for any state-changing endpoint — JWTs are TS-signed and the server
  // validates every request. The bell + notifications can keep reading
  // (unauth reads return 401, the badge falls back to 0 unread).
  // v23.2 — WalletConnect-only. The previous default of 'injected'
  // propagated the legacy MetaMask-pair kind, which the user-facing
  // saved-wallet pill AND the silent reconnect branch both gated on.
  // Default to '' (the empty string) so old browsers with addr-but-no-kind
  // records would have hasSavedWallet=false at load — the Connect Wallet
  // button is the only entry point. Returning users who had paired via
  // MetaMask (savedKind='injected') are migrated: their session kind is
  // overwritten on the next successful WC pair, and until then the saved
  // pill is hidden (UX consistency with the WC-only contract).
  try {
    const addr = localStorage.getItem('mw_addr');
    const kind = localStorage.getItem('mw_kind') || '';
    if (addr) {
      const w = Alpine.store('wallet');
      w.savedAddress = addr;
      w.savedKind    = kind;
    }
  } catch (_) {}
});

/* ─────────────────────────────────────────────────────────────────────────────
 * Toast notifications (5-color palette)
 * ───────────────────────────────────────────────────────────────────────────── */
function toast(msg, type = 'info') {
  const styles = {
    success: 'border-gold-300/50 text-ink-950 font-extrabold glow-gold',
    error:   'bg-ink-1000/95 text-red-200 border border-red-400/40',
    info:    'bg-sky-500/15 text-sky-100 border border-sky-300/40 backdrop-blur',
  };
  const el = document.createElement('div');
  el.setAttribute('role', type === 'error' ? 'alert' : 'status');
  el.className = `pointer-events-auto px-5 py-3.5 rounded-2xl text-sm font-semibold shadow-xl shadow-black/40 transition-all duration-300 ${styles[type] || styles.info}`;
  el.textContent = msg;
  document.getElementById('toasts')?.appendChild(el);
  setTimeout(() => { el.style.opacity = '0'; el.style.transform = 'translateY(8px)'; }, 3600);
  setTimeout(() => el.remove(), 4000);
}

/* ─────────────────────────────────────────────────────────────────────────────
 * Event bus → wallet action routing
 * ───────────────────────────────────────────────────────────────────────────── */
// Listing-card Buy click reaches `document` via $dispatch bubbled CustomEvent;
// previous `window.addEventListener` silently never fired from inside the
// listing card's nested DOM, so the click fell through to the wrapping
// <a href="/token/..."> navigation. `document` is reliably on the bubble path.
document.addEventListener('buy', e => {
  const { collection, tokenId, seller, price } = e.detail || {};
  if (collection && tokenId && seller && price) {
    Alpine.store('wallet').buy(collection, tokenId, seller, price);
  }
});
document.addEventListener('cancel-listing', e => {
  const { collection, tokenId } = e.detail || {};
  Alpine.store('wallet').cancel(collection, tokenId);
});
document.addEventListener('settle-auction', e => {
  Alpine.store('wallet').settle(e.detail.auctionId);
});

window.addEventListener('mw-notification', () => {
  const w = Alpine.store('wallet');
  if (w?.jwt) w.refreshUnread();
});

/* ─────────────────────────────────────────────────────────────────────────────
 * URI helpers — used by inline JS in templates
 * ───────────────────────────────────────────────────────────────────────────── */
function isBareIPFSCID(uri) {
  return (uri.startsWith('Qm') && uri.length >= 44) || (uri.startsWith('baf') && uri.length >= 59);
}
function resolveURI(uri) {
  uri = (uri || '').trim();
  if (!uri || uri.startsWith('data:')) return uri;
  if (uri.startsWith('ipfs://ipfs/')) return 'https://ipfs.io/ipfs/' + uri.slice(13);
  if (uri.startsWith('ipfs://'))      return 'https://ipfs.io/ipfs/' + uri.slice(7);
  if (isBareIPFSCID(uri))             return 'https://ipfs.io/ipfs/' + uri;
  if (uri.startsWith('ar://'))        return 'https://arweave.net/' + uri.slice(5);
  return uri;
}
function mediaURL(uri) {
  if (!uri) return uri;
  if (uri.startsWith('/api/v1/img/') || uri.startsWith('data:') || uri.startsWith('/')) return uri;
  if (uri.startsWith('http://') || uri.startsWith('https://')) {
    return '/api/v1/media?url=' + encodeURIComponent(uri);
  }
  const resolved = resolveURI(uri);
  if (resolved.startsWith('http://') || resolved.startsWith('https://')) {
    return '/api/v1/media?url=' + encodeURIComponent(resolved);
  }
  return uri;
}

// Expose globals for inline Alpine / template JS calls. The IIFE runs
// synchronously when this script is parsed, which is BEFORE alpinejs has
// loaded (layout.html load order: htmx → sse → ethers → wallet.js →
// qrcode.min.js → alpine.js, all `defer`). Alpine is therefore undefined
// at this exact moment — DO NOT touch `window.Alpine` here; Alpine's own
// UMD bootstrap will install it. Pre-setting `window.Alpine = undefined`
// would race against later bundles that guard with `if (!window.Alpine)`
// and break the registration in some Alpine builds.

// ────────────────────────────────────────────────────────────────────────
// Force-hide ALL modals/dropdowns — global kill-switch.
//
// Bug class (v17): an Alpine `x-transition.opacity` mid-flight that
// gets interrupted by a tab visibility change can leave a dropdown
// visually frozen onscreen even though its reactive flag is false. The
// DOM `display:` style never gets set because Alpine's transition
// listener is awaiting the next anim frame that's never going to arrive.
// The user sees a stuck dropdown that "won't close" even though every
// @click handler, every X button, every outside-click has already
// fired. Worse: on tab-switch BACK to the page the same stale state
// greets the user, who concludes "frozen across tabs".
//
// `MW_HIDE_ALL()` is the belt-and-braces disarm: every dropdown flag
// that is safe to close in isolation flips to false, plus each modal
// closes via its own defensive path (mw-wc-hide event, NFT picker
// hide event, modal dismiss). Safe-to-close == not mid-confirmation
// (step < 1). The action_modal.killSwitch flag distinguishes "user
// dropped the modal mid-cancel" from "tx is signing in the wallet
// and we MUST NOT cancel" (step >= 1).
//
// Additionally: a `visibilitychange` listener below calls this on every
// tab-focus return so any DOM state that was wedged by a mid-flight
// transition when the tab was hidden gets torn down before the user
// sees it.
// ────────────────────────────────────────────────────────────────────────
function MW_HIDE_ALL() {
  // 1. Navbar/local-state dropdowns. We grab the nav's x-data via the
  //    DOM and force-evaluate its data — more robust than assuming
  //    global nav.x because every page loads layout.html with its own
  //    nav instance.
  const nav = document.querySelector('nav[x-data]');
  if (nav && nav._x_dataStack && nav._x_dataStack[0]) {
    const d = nav._x_dataStack[0];
    if (d && typeof d === 'object') {
      try { if (d.wcOpen)    d.wcOpen    = false; } catch (_) {}
      try { if (d.openBell)  d.openBell  = false; } catch (_) {}
      try { if (d.open)      d.open      = false; } catch (_) {}
    }
  }
  // 2. WC QR overlay (event-bus driven, no nav-scope tie).
  try { window.dispatchEvent(new CustomEvent('mw-wc-hide')); } catch (_) {}
  // 3. NFT picker.
  try { window.dispatchEvent(new CustomEvent('mw-nft-picker-hide')); } catch (_) {}
  // 4. Action modal — only when NOT in the middle of a wallet signing
  //    confirmation. The store's dismiss() callback is the canonical
  //    path; we guard with step < 1 explicitly so an in-flight buy
  //    (step >= 1) is NEVER touched (the user can't cancel a
  //    signed tx from the server anyway, but dismissing the modal
  //    mid-flight leaves them with no UI feedback for the tx).
  try {
    const m = typeof Alpine !== 'undefined' && Alpine.store && Alpine.store('modals');
    if (m && m.open && typeof m.step === 'number' && m.step < 1) {
      m.dismiss();
    }
  } catch (_) {}
  // 5. CSS-level DOM poke: any modal-root that has `style="display:none"`
  //    already at init gets stamped now via inline style override so a
  //    wedged `x-transition` cannot hold the dropdown onscreen even
  //    if every reactive flag is correctly false. Belt vs. the known
  //    "Alpine transition freezing on tab-hide" race.
  try {
    const ids = ['wc-modal-root', 'nft-picker-modal-root', 'mw-modal-killer'];
    for (const id of ids) {
      const el = document.getElementById(id);
      if (el) el.style.display = 'none';
    }
    // The action modal's root div is unnamed in action_modal.html
    // (wrapped in <template x-if="true">) — pattern-match its class.
    const actionRoot = document.querySelector('div.fixed.inset-0.z-\\[70\\]');
    if (actionRoot) actionRoot.style.display = 'none';
  } catch (_) {}
}
window.MW_HIDE_ALL = MW_HIDE_ALL;

// visibilitychange: when the tab comes BACK into focus, force-hide
// anything that was wedged while the tab was hidden. The browser
// pauses `requestAnimationFrame` on hidden tabs, which is the trigger
// for the x-transition freeze class. We listen on document because
// `window.addEventListener('visibilitychange', ...)` and
// `document.addEventListener('visibilitychange', ...)` are equivalent
// — both fire while the document is in the foreground or background.
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      // Wait one microtask so Alpine finishes any binding-flush that
      // accumulated during the hidden phase before we tear state down.
      Promise.resolve().then(() => MW_HIDE_ALL());
    }
  });
}


// persistent Scan-QR-on-your-phone chip in the navbar. Idempotent:
// if window.MW_WC_URI is cached from a prior display_uri emission,
// dispatch mw-wc-show carrying that URI so the overlay rehydrates the
// same QR. Otherwise dispatch mw-wc-show { loading: true } so the
// overlay shows the spinner (a fresh _wcConnect should be in flight).
// Only meaningful when a WalletConnect pairing session is currently in
// flight; dispatching it outside that context would be a no-op.
function MW_WC_OPEN_OVERLAY() {
  try {
    if (window.MW_WC_URI && typeof window.MW_WC_URI === 'string'
        && window.MW_WC_URI.startsWith('wc:')) {
      window.dispatchEvent(new CustomEvent('mw-wc-show', { detail: { uri: window.MW_WC_URI } }));
      // Legacy alias.
      window.dispatchEvent(new CustomEvent('mw-wc-uri', { detail: window.MW_WC_URI }));
    } else {
      window.dispatchEvent(new CustomEvent('mw-wc-show', { detail: { loading: true } }));
      window.dispatchEvent(new CustomEvent('mw-wc-connecting'));
    }
  } catch (_) {}
}
window.MW_WC_OPEN_OVERLAY = MW_WC_OPEN_OVERLAY;


}());
