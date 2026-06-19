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

  // Path 3 — full EIP-1193 reconstruction (last-resort)
  let eip1193 = null;
  try { eip1193 = R(store?._raw?.wc) || store?._raw?.wc || null; } catch (_) {}
  if (!eip1193) eip1193 = window.ethereum || null;
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
  // Fallback: WC provider OR injected window.ethereum
  let eip = R(store?._raw?.wc) || store?._raw?.wc || window.ethereum;
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
     */
    open(opts) {
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

    get provider() { return this._raw.provider; },
    get signer()   { return this._raw.signer;   },
    set provider(v) { this._raw.provider = R(v); },
    set signer(v)   { this._raw.signer   = R(v); },

    get shortAddr() {
      return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : '';
    },
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

    // ── Connect (injected wallet OR WalletConnect v2 via QR) ──

    async connect(kind = 'injected', { silent = false } = {}) {
      if (this.state === 'connecting') return;
      const wasError = this.state === 'error';
      this.setState('connecting');
      try {
        let eip1193;
        if (kind === 'walletconnect') {
          if (!WC_PROJECT_ID) throw new Error('WalletConnect is not configured on this server.');
          eip1193 = await this._wcConnect();
        } else {
          if (!window.ethereum) {
            this.setState('idle');
            if (!silent) toast('No injected wallet found. Install MetaMask or use WalletConnect.', 'error');
            return;
          }
          eip1193 = window.ethereum;
        }
        const provider = new ethers.BrowserProvider(eip1193);
        const accounts = await provider.send('eth_requestAccounts', []);
        if (!accounts?.length) throw new Error('No account authorized.');
        const network = await provider.getNetwork();
        if (kind === 'injected' && Number(network.chainId) !== CHAIN_ID) {
          try { await this._switchChain(); } catch (_) {}
        }
        // Always store ROOT ethers objects unwrapped. Setters nested-call
        // R() so double-wrap is impossible.
        this._raw.provider = R(provider);
        this._raw.signer   = R(await provider.getSigner());
        this.address       = accounts[0].toLowerCase();
        this.chainId       = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        localStorage.setItem('mw_kind', kind);

        if (kind === 'walletconnect' && eip1193?.on) {
          eip1193.on('disconnect', () => this.disconnect());
          eip1193.on('chainChanged', () => location.reload());
          eip1193.on('accountsChanged', (accs) => {
            if (!accs.length) this.disconnect();
            else {
              this.address = accs[0].toLowerCase();
              localStorage.setItem('mw_addr', this.address);
            }
          });
        }

        await this._authenticate();
        await this.refreshUnread();
        this.setState('connected');
        if (!silent) toast(kind === 'walletconnect'
          ? 'Connected via WalletConnect'
          : 'Wallet connected', 'success');
      } catch (e) {
        this.setState('error', { error: e });
        if (wasError || !silent) toast(revertMessage(e), 'error');
      }
    },

    // WalletConnect v2 — owns the entire pairing UX through the
    // partials/wc_qr_overlay.html overlay (we render our own QR matrix
    // via the self-hosted qrcode.min.js encoder). The SDK's built-in
    // modal was disabled because:
    //   (a) it pops up INSTANTLY on init — was the source of the
    //       popup-instantly-show-up complaint;
    //   (b) it fetches assets from walletconnect.com which is blocked
    //       on some networks / policies, leaving a blank box where the
    //       QR should be — was the no-QR-showing complaint;
    //   (c) its Got it affordance is not tuned to our 5-color palette.
    //
    // Sequencing: dispatch mw-wc-connecting BEFORE await init so the
    // overlay can show a spinner — defeats the blank-flash race. Attach
    // the display_uri listener IMMEDIATELY after init resolves and
    // BEFORE wc.connect() so the SDK's buffered re-emission of
    // display_uri on late subscriber attach reaches us.
    async _wcConnect() {
      try { window.dispatchEvent(new CustomEvent('mw-wc-connecting')); } catch (_) {}
      let wc;
      try {
        const mod = await import('https://esm.sh/@walletconnect/ethereum-provider@2.14.0?bundle');
        wc = await mod.EthereumProvider.init({
          projectId: WC_PROJECT_ID,
          chains:    [CHAIN_ID],
          rpcMap:    { [CHAIN_ID]: RPC_URL },
          showQrModal: false,
          metadata: {
            name: 'MagicWebb',
            description: 'Non-custodial NFT marketplace on Flare Network',
            url: window.location.origin,
            icons: [`${window.location.origin}/static/icon-512.png`],
          },
        });
      } catch (e) {
        throw new Error('WalletConnect failed to load: ' + (e?.message || e));
      }
      this._raw.wc = R(wc);

      wc.on('display_uri', (uri) => {
        window.MW_WC_URI = uri;
        try {
          window.dispatchEvent(new CustomEvent('mw-wc-uri', { detail: uri }));
        } catch (_) {}
      });

      try {
        await wc.connect();
      } catch (e) {
        try { wc.disconnect(); } catch (_) {}
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
    },

    async _switchChain() {
      if (!window.ethereum?.request) return;
      await window.ethereum.request({
        method: 'wallet_switchEthereumChain',
        params: [{ chainId: '0x72' }],
      }).catch(async () => {
        await window.ethereum.request({
          method: 'wallet_addEthereumChain',
          params: [{
            chainId: '0x72',
            chainName: 'Coston2',
            nativeCurrency: { name: 'C2FLR', symbol: 'C2FLR', decimals: 18 },
            rpcUrls: [RPC_URL],
            blockExplorerUrls: [EXPLORER],
          }],
        });
      });
    },

    async _authenticate() {
      try {
        const nonceRes = await fetch('/auth/nonce?address=' + this.address);
        if (!nonceRes.ok) return;
        const { nonce } = await nonceRes.json();
        const message = `Sign in to MagicWebb\nAddress: ${this.address}\nNonce: ${nonce}`;
        const sig = await R(this.signer).signMessage(message);
        const verifyRes = await fetch('/auth/verify', {
          method:  'POST',
          headers: { 'Content-Type': 'application/json' },
          body:    JSON.stringify({ address: this.address, message, signature: sig }),
        });
        if (!verifyRes.ok) return;
        const { token } = await verifyRes.json();
        this.jwt = token;
        localStorage.setItem('mw_jwt', token);
      } catch (e) { console.warn('SIWE auth failed:', e); }
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
      this._toast('Approve in your wallet…', 'info');
      const tx = await writeContract.setApprovalForAll(operator, true);
      await tx.wait();
      this._toast('Approved.', 'success');
      return true;
    },

    // ── ensureSigner — the canonical signer-acquisition path ──

    // Returns `null` on any failure so callers can short-circuit with a
    // clear "Connect your wallet first" message rather than letting Ethers
    // throw its AbstractProvider error mid-flow.
    async ensureSigner() {
      if (!this.signer) {
        await this.connect(localStorage.getItem('mw_kind') || 'injected', { silent: true });
      }
      if (!this.signer) return null;
      const s = await resolveSigner(this);
      if (s) {
        this._raw.signer = s;
        return s;
      }
      // Last-resort: a fresh full reconnect before declaring failure.
      try {
        await this.connect(localStorage.getItem('mw_kind') || 'injected');
      } catch (_) {}
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
        this._toast('Another action is already in progress — please wait.', 'info');
        return { ok: false, error: 'busy' };
      }
      this.busy = true;
      try {
        const signer = await this.ensureSigner();
        if (!signer) {
          return await Alpine.store('modals').open({
            kind: 'list', icon: '⚡',
            title: 'Connect your wallet first',
            subtitle: opts.subtitle || '',
            summary: [{ label: 'Action', value: opts.title || 'Continue' }],
            disclaimer: 'Connection is never custodial — your keys stay in your wallet.',
            ctaLabel: 'Got it',
            run: async ({ fail, setStep }) => {
              setStep(1, 'Open the wallet picker…');
              this._toast('Click "Connect Wallet" above to choose a wallet.', 'info');
              fail({ title: 'Wallet not connected', body: 'Connect a wallet to continue.' });
            },
          });
        }
        const provider = await resolveProvider(this);
        return await Alpine.store('modals').open({
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
        const contract = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await contract.buy(collection, tokenId, seller, { value: BigInt(priceWei) });
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
        this._toast('Report submitted. Thank you.', 'success');
      } catch (_) { this._toast('Could not submit report.', 'error'); }
    },
    async saveProfile(fields) {
      try {
        const res = await fetch(`/api/v1/profile/${this.address}`, {
          method: 'PUT', headers: this.authHeaders(), body: JSON.stringify(fields),
        });
        if (!res.ok) throw new Error('save failed');
        this._toast('Profile saved.', 'success');
      } catch (_) { this._toast('Could not save profile.', 'error'); }
    },

    _toast(msg, type = 'info') { return toast(msg, type); },
  });

  // ── Auto-reconnect on page load — silent path. ──
  const saved = localStorage.getItem('mw_addr');
  const kind  = localStorage.getItem('mw_kind') || 'injected';
  if (saved && (kind === 'walletconnect' ? WC_PROJECT_ID : !!window.ethereum)) {
    Alpine.store('wallet').connect(kind, { silent: true }).catch(() => {});
  }
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
window.addEventListener('buy', e => {
  const { collection, tokenId, seller, price } = e.detail || {};
  if (collection && tokenId && seller && price) {
    Alpine.store('wallet').buy(collection, tokenId, seller, price);
  }
});
window.addEventListener('cancel-listing', e => {
  const { collection, tokenId } = e.detail || {};
  Alpine.store('wallet').cancel(collection, tokenId);
});
window.addEventListener('settle-auction', e => {
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
window.fmtFLR  = fmtFLR;
window.fmtAddr = fmtAddr;
window.mediaURL = mediaURL;

// Re-open the WalletConnect QR overlay from anywhere (notably the
// persistent Scan-QR-on-your-phone chip in the layout navbar). Idempotent:
// if window.MW_WC_URI is cached from a prior display_uri emission,
// dispatch mw-wc-uri so the overlay rehydrates with that QR. Otherwise
// dispatch mw-wc-connecting so the overlay shows its spinner / wait
// state. Wallet state stays connecting throughout \u2014 wc.connect() in
// _wcConnect() drives the actual handshake.
function MW_WC_OPEN_OVERLAY() {
  try {
    if (window.MW_WC_URI && typeof window.MW_WC_URI === 'string'
        && window.MW_WC_URI.startsWith('wc:')) {
      window.dispatchEvent(new CustomEvent('mw-wc-uri', { detail: window.MW_WC_URI }));
    } else {
      window.dispatchEvent(new CustomEvent('mw-wc-connecting'));
    }
  } catch (_) {}
}
window.MW_WC_OPEN_OVERLAY = MW_WC_OPEN_OVERLAY;
}());
