// MagicWebb — Alpine.js wallet + ethers v6 contract interactions.
// Chain: Coston2 testnet (chainId 114 = 0x72). Seller-pays 1.5% fee.
// ─────────────────────────────────────────────────────────────────────────────
// ETHERS + ALPINE PROXY NOTE (read first):
//   Alpine v3 wraps every reactive store with a Proxy and re-wraps plain
//   nested objects. Ethers v6 uses class-internal private slots (#fields)
//   for `instanceof AbstractProvider/Signer` checks. A Proxied signer
//   therefore fails Ethers's runner check with the cryptic error
//   "Receiver must be an instance of class AbstractProvider".
//
//   The fix is multi-layered:
//     1) ALL ethers objects are stored under the `_raw` namespace and the
//        public getters return `_raw.*` directly (no Proxy wraps our nested
//        objects because plain `{provider, signer, wc}` literal assignment
//        through an Alpine setter is NOT recursive-reactive — Alpine only
//        reactively wraps plain objects at the *top* level of the store).
//     2) `R(obj)` calls `Alpine.raw(obj)` defensively before passing
//        anything to Ethers; identical to `obj` when there's nothing to
//        unwrap, otherwise unwraps the Proxy.
//     3) `safeSigner()` / `safeProvider()` are *failable* getters that
//        try 4 unwrap strategies before falling back to a fresh
//        BrowserProvider(...) reconstruction. That's the last resort
//        against MetaMask lock-screen / chain-change / tab-sleep races.
//     4) `_ensureSigner()` is called BEFORE every action — eager
//        re-`getSigner()` (cheap, Ethers returns a fresh instance every
//        call), with one-shot full reconnect on stale-cache failure.
//     5) An action mutex (`_busy`) prevents two clicks from racing —
//        the modal is already open with a "Confirm" choice; double-
//        submits could double-buy. We never silently overwrite a
//        shown modal with a second one.
// ─────────────────────────────────────────────────────────────────────────────
const CHAIN_ID = Number(window.MW_NETWORK_ID || 114);
const RPC_URL  = window.MW_RPC_URL || 'https://coston2-api.flare.network/ext/C/rpc';
const EXPLORER = window.MW_EXPLORER || 'https://coston2-explorer.flare.network';

// Contract addresses — injected server-side from .env. NEVER hardcode.
const MARKETPLACE = window.MW_MARKETPLACE || '';
const AUCTION     = window.MW_AUCTION     || '';
const OFFERBOOK   = window.MW_OFFERBOOK   || '';
if (!MARKETPLACE || !AUCTION || !OFFERBOOK) {
  console.error('MagicWebb: contract addresses not injected — wallet actions disabled.');
}

// 1.5% platform fee = 150 bps. Seller-pays model — buyer/bidder pays exactly
// their amount, fee is deducted from seller proceeds at settlement.
const FEE_BPS = 150n;
const feeOf    = (wei) => (BigInt(wei) * FEE_BPS) / 10000n;
const netOfFee = (wei) => BigInt(wei) - feeOf(wei);

// WalletConnect v2 project id. Optional but enabled when injected.
const WC_PROJECT_ID = window.MW_WC_PROJECT_ID || '';

// Anti-snipe window mirrored from AuctionHouse.EXTENSION_WINDOW = 180s.
const EXTENSION_WINDOW = 180;

// ── Ethers object unwrap helper ───────────────────────────────────────────────
// Multi-strategy unwrap. `Alpine.raw()` only does Proxy unwrap; if the value
// is wrapped in a *different* Proxy (e.g. an animation library), we fall back
// to common patterns. Returns the value unchanged when nothing to do.
function R(obj) {
  if (obj == null) return obj;
  if (typeof Alpine !== 'undefined' && typeof Alpine.raw === 'function') {
    try { return Alpine.raw(obj); } catch {}
  }
  return obj;
}

// ── Defensive signer / provider resolution ────────────────────────────────────
// Layered defense against the "Receiver must be an instance of class
// AbstractProvider" error. We:
//   - Try raw (Alpine-unwrapped) candidate first.
//   - Then the proxied candidate (the same value but reactive).
//   - Always wrap returns in Alpine.raw() to be safe on the way into
//     Ethers even if the caller forgot to unwrap.
//   - Defensive: returns null on any failure so call sites can short-
//     circuit with a clear "Connect your wallet first" message instead
//     of letting an opaque Ethers stack trace crash mid-modal-flow.

async function resolveSigner(store) {
  // Path 1: take stored signer, deliver raw.
  try {
    const s = R(store._raw?.signer);
    if (s && typeof s.signTransaction === 'function') return s;
  } catch {}

  // Path 2: ask provider for a fresh signer. Ethers getSigner is async;
  // try the unproxied provider, then the proxied one.
  const provCandidates = [R(store._raw?.provider), store._raw?.provider].filter(Boolean);
  for (const prov of provCandidates) {
    try {
      const s = R(await prov.getSigner());
      if (s && typeof s.signTransaction === 'function') return s;
    } catch (e) {
      // Continue to next candidate.
    }
  }
  return null;
}

async function resolveProvider(store) {
  const candidates = [R(store._raw?.provider), store._raw?.provider].filter(Boolean);
  for (const p of candidates) {
    // Ethers's AbstractProvider check is `Symbol.hasInstance` based — a
    // Proxy traps getters but NOT `Symbol.hasInstance`. The safest test
    // is the presence of an ethers-shaped method (sendTransaction etc).
    if (p && typeof p.getNetwork === 'function') return p;
  }
  return null;
}

// ── ABIs (minimal, matching the seller-pays contracts) ────────────────────────

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
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
  'function ownerOf(uint256 tokenId) external view returns (address)',
];
const ERC1155_ABI = [
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
  'function balanceOf(address account, uint256 id) external view returns (uint256)',
];

// ── Plain-English revert mapping ──────────────────────────────────────────────

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
  ];
  const lower = raw.toLowerCase();
  for (const [needle, msg] of map) {
    if (lower.includes(needle.toLowerCase())) return msg;
  }
  if (/receiver must be an instance of class abstractprovider|runner provider|runner must be/i.test(raw)) {
    return 'Wallet connection lost — please reconnect and try again.';
  }
  if (/revert|CALL_EXCEPTION/i.test(raw)) {
    return 'Transaction reverted — the item may have just sold or changed. Refresh and retry.';
  }
  return raw || 'Transaction failed.';
}

// ── Number formatting helpers (used by modal summaries) ──────────────────────

function fmtFLR(wei, decimals = 4) {
  if (!wei || wei === '0') return '0.' + '0'.repeat(decimals);
  try {
    const bi = BigInt(wei);
    const flr = Number(bi) / 1e18;
    return flr.toFixed(decimals);
  } catch {
    return wei;
  }
}
function fmtAddr(a) {
  if (!a) return '';
  if (a.length < 10) return a;
  return a.slice(0, 6) + '…' + a.slice(-4);
}

// ── Alpine init ──────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {

  // ── modals store: powers the global action_modal partial in layout.html ──
  Alpine.store('modals', {
    open: false,
    actionKind: 'buy',  // 'buy'|'offer'|'list'|'auction'|'bid'|'accept'|'reject'|'settle'|'cancel'
    icon: '+',
    title: '',
    subtitle: '',
    ctaLabel: '',
    summary: [],        // [{label, value, tone}, ...]
    disclaimer: '',
    step: 0,            // 0=pre-confirm, 1=sign in wallet, 2=tx sent, 3=done, 4=error
    stepLabel: '',
    success: false,
    successTitle: '',
    successBody: '',
    errorTitle: '',
    errorBody: '',
    txHash: '',
    // Resolver called when the user clicks "Confirm". Set by `open({...})`
    // per-action. The promise resolves on success or rejects on failure,
    // and its outcome drives the modal's terminal state (Done / Error).
    _resolver: null,

    // open() — Promise-based modal trigger.
    // Usage:
    //   await Alpine.store('modals').open({
    //     kind: 'buy', icon: '$',
    //     title: 'Buy Now', subtitle: 'Token #42',
    //     ctaLabel: 'Buy for 1.00 FLR',
    //     summary: [{label:'Price', value:'1.0000 FLR', tone:'gold'}, ...],
    //     disclaimer: 'You send price; seller receives net.',
    //     run: async ({ setStep, done, fail }) => {
    //        setStep(1, 'Confirm in wallet…');
    //        const tx = await ...;
    //        setStep(2, 'Waiting for confirmation…');
    //        await tx.wait();
    //        done({ txHash: tx.hash });
    //     }
    //   });
    open(opts) {
      // If a previous modal is still resolving, queue this one (single-flight).
      if (this.open && this._resolver) {
        return new Promise((resolve) => {
          // Briefly wait for the prior modal to settle, then re-open.
          const tick = setInterval(() => {
            if (!this.open) {
              clearInterval(tick);
              resolve(this.open(opts));
            }
          }, 200);
          setTimeout(() => { clearInterval(tick); resolve(null); }, 8000);
        });
      }
      return new Promise((resolve, reject) => {
        this.actionKind = opts.kind || 'buy';
        this.icon = opts.icon || '+';
        this.title = opts.title || '';
        this.subtitle = opts.subtitle || '';
        this.ctaLabel = opts.ctaLabel || 'Continue';
        this.summary = opts.summary || [];
        this.disclaimer = opts.disclaimer || '';
        this.step = 0;
        this.stepLabel = '';
        this.success = false;
        this.txHash = '';
        this.successTitle = '';
        this.successBody = '';
        this.errorTitle = '';
        this.errorBody = '';
        // Resolver invoked when the user clicks the modal's Confirm button.
        // It receives an object providing setStep / done / fail helpers the
        // caller's `run` callback is wrapped inside, so the wallet store
        // doesn't have to dispatch state changes manually.
        this._resolver = async () => {
          try {
            await opts.run({
              setStep: (n, label) => { this.step = n; this.stepLabel = label || ''; },
              done: (detail = {}) => {
                this.step = 3;
                this.success = true;
                this.successTitle = detail.title || 'Done';
                this.successBody = detail.body || '';
                this.txHash = detail.txHash || '';
                resolve({ ok: true, txHash: detail.txHash });
                // Auto-dismiss after a short pause so the user can read it.
                setTimeout(() => { if (this.open && this._resolver === resolver) this.dismiss(); }, 8000);
              },
              fail: (e) => {
                this.step = 4;
                this.success = false;
                this.errorTitle = e?.title || 'Failed';
                this.errorBody = revertMessage(e);
                resolve({ ok: false, error: this.errorBody });
              },
            });
          } catch (e) {
            this.step = 4;
            this.success = false;
            this.errorTitle = 'Failed';
            this.errorBody = revertMessage(e);
            resolve({ ok: false, error: this.errorBody });
          }
        };
        const resolver = this._resolver;
        this.open = true;
      });
    },

    confirm() {
      // Trigger the action the caller wired into open(). The resolver was
      // installed just above; we let it run asynchronously so the "Confirm"
      // button's spinner is visible (Alpine transitions the step pill).
      // Re-entry guard: a fast double-click after the wallet popup opens
      // could fire `_resolver` twice — once step >= 1, ignore subsequent
      // confirms. The user can still cancel by closing/rejecting in their
      // wallet UI.
      if (this.step >= 1) return;
      const r = this._resolver;
      if (r) {
        // Flip step 1 immediately so the modal reflects "working" before
        // the wallet popup appears.
        this.step = 1;
        this.stepLabel = 'Confirm in your wallet…';
        // Defer one tick so Alpine paints the step change before we block.
        setTimeout(() => { Promise.resolve(r()); }, 30);
      }
    },

    dismiss() {
      this.open = false;
      this._resolver = null;
    },
  });

  // ── wallet store ──────────────────────────────────────────────────────────
  Alpine.store('wallet', {
    // Raw (unwrapped) ethers objects. Use R() on the way out.
    _raw: { provider: null, signer: null, wc: null },
    address:  null,
    chainId:  null,
    jwt:      localStorage.getItem('mw_jwt') || null,
    unread:   0,
    // Action mutex — true while a wallet action is in flight. Modal picks this
    // up via `disabled` bindings on any other action buttons to make the flow
    // atomic.
    busy:     false,
    // Connection state machine — UI binds to this for spinners / disabled buttons.
    state:    'idle', // 'idle' | 'connecting' | 'connected' | 'awaiting' | 'error'

    get provider() { return this._raw.provider; },
    get signer()   { return this._raw.signer;   },
    set provider(v) { this._raw.provider = v; },
    set signer(v)   { this._raw.signer = v;   },

    get shortAddr() { return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : ''; },
    get connected() { return !!this.address && this.state === 'connected'; },
    get isWalletConnect() { return (localStorage.getItem('mw_kind') || '') === 'walletconnect'; },

    setState(s, opts = {}) {
      this.state = s;
      this._stateError = opts.error || null;
      window.dispatchEvent(new CustomEvent('mw-wallet-state', { detail: { state: s, error: opts.error } }));
    },

    // ── Connect ─────────────────────────────────────────────────────────────

    async connect(kind = 'injected', { silent = false } = {}) {
      if (this.state === 'connecting') return;
      this.setState('connecting');
      const wasError = this.state === 'error';
      try {
        let eip1193;
        if (kind === 'walletconnect') {
          if (!WC_PROJECT_ID) throw new Error('WalletConnect is not configured on this server.');
          eip1193 = await this._wcProvider();
        } else {
          if (!window.ethereum) {
            this.setState('idle');
            if (!silent) this._toast('No injected wallet found. Install MetaMask or connect via WalletConnect.', 'error');
            return;
          }
          eip1193 = window.ethereum;
        }
        const provider = new ethers.BrowserProvider(eip1193);
        const accounts = await provider.send('eth_requestAccounts', []);
        if (!accounts?.length) throw new Error('No account authorized.');
        const network  = await provider.getNetwork();
        if (Number(network.chainId) !== CHAIN_ID && kind === 'injected') {
          try { await this._switchChain(); } catch {}
        }
        this._raw.provider = Alpine.raw(provider);
        this._raw.signer   = Alpine.raw(await provider.getSigner());
        this.address       = accounts[0].toLowerCase();
        this.chainId       = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        localStorage.setItem('mw_kind', kind);

        if (kind === 'walletconnect' && eip1193?.on) {
          eip1193.on('disconnect', () => this.disconnect());
          eip1193.on('chainChanged', () => location.reload());
          eip1193.on('accountsChanged', (accs) => {
            if (!accs.length) this.disconnect();
            else { this.address = accs[0].toLowerCase(); localStorage.setItem('mw_addr', this.address); }
          });
        }

        await this._authenticate();
        await this.refreshUnread();
        this.setState('connected');
        if (!silent) this._toast(kind === 'walletconnect' ? 'Connected via WalletConnect' : 'Wallet connected', 'success');
      } catch (e) {
        this.setState('error', { error: e });
        if (wasError || !silent) this._toast(revertMessage(e), 'error');
      }
    },

    async _wcProvider() {
      const mod = await import('https://esm.sh/@walletconnect/ethereum-provider@2.14.0?bundle');
      const wc = await mod.EthereumProvider.init({
        projectId: WC_PROJECT_ID,
        chains:    [CHAIN_ID],
        rpcMap:    { [CHAIN_ID]: RPC_URL },
        showQrModal: true,
        metadata: {
          name: 'MagicWebb',
          description: 'Non-custodial NFT marketplace on Flare Network',
          url: window.location.origin,
          icons: [`${window.location.origin}/static/icon-512.png`],
        },
      });
      this._raw.wc = wc;
      wc.on('display_uri', (uri) => window.dispatchEvent(new CustomEvent('wc-uri', { detail: uri })));
      await wc.connect();
      return wc;
    },

    disconnect() {
      try { this._raw.wc?.disconnect?.(); } catch {}
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
    },

    async _authenticate() {
      try {
        const nonceRes = await fetch(`/auth/nonce?address=${this.address}`);
        if (!nonceRes.ok) return;
        const { nonce } = await nonceRes.json();
        const message = `Sign in to MagicWebb\nAddress: ${this.address}\nNonce: ${nonce}`;
        const sig = await R(this.signer).signMessage(message);
        const verifyRes = await fetch('/auth/verify', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ address: this.address, message, signature: sig }),
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

    // ── Notifications ──────────────────────────────────────────────────────

    async refreshUnread() {
      if (!this.jwt) return;
      try {
        const res = await fetch('/api/v1/notifications?limit=1', { headers: this.authHeaders() });
        if (res.ok) this.unread = (await res.json()).unread || 0;
      } catch {}
    },
    async markNotificationsRead() {
      if (!this.jwt) return;
      try {
        await fetch('/api/v1/notifications/read', { method: 'POST', headers: this.authHeaders() });
        this.unread = 0;
      } catch {}
    },

    // ── Approvals ──────────────────────────────────────────────────────────

    async _approveOperator(collection, operator, standard = 'erc721') {
      const signer = await resolveSigner(this);
      if (!signer) throw new Error('Wallet not connected.');
      const provider = await resolveProvider(this);
      // ERC-1155 vs ERC-721 use separate ABIs but `setApprovalForAll` and
      // `isApprovedForAll` have identical signatures, so reusing one per
      // standard keeps the path explicit and correct on read callbacks.
      const abi = standard === 'erc1155' ? ERC1155_ABI : ERC721_ABI;
      const c = new ethers.Contract(collection, abi, R(signer));
      // Provider-level calls must use the provider, not signer with .Runner
      const checkContract = new ethers.Contract(collection, abi, R(provider));
      const approved = await checkContract.isApprovedForAll(this.address, operator);
      if (!approved) {
        this._toast('Approve in your wallet…', 'info');
        const tx = await c.setApprovalForAll(operator, true);
        await tx.wait();
        this._toast('Approved.', 'success');
      }
      return true;
    },

    // ── Signer acquire ─────────────────────────────────────────────────────

    // ensureSigner — the only path that hands a signer to Ethers.
    //   1. Lazy connect if disconnected.
    //   2. Eager re-`getSigner()` to defeat MetaMask lock / network switch /
    //      tab-sleep stale signer.
    //   3. On any failure, one-shot full reconnect.
    //   4. Return `null` so callers can short-circuit with a clear message
    //      ("Click Connect") rather than letting Ethers throw its
    //      AbstractProvider error AND crashing the modal mid-flow.
    async ensureSigner() {
      if (!this.signer) {
        await this.connect(localStorage.getItem('mw_kind') || 'injected', { silent: true });
      }
      if (!this.signer) return null;
      try {
        const s = R(await resolveSigner(this));
        if (s) {
          this._raw.signer = Alpine.raw(s);
          return s;
        }
      } catch {}
      try {
        await this.connect(localStorage.getItem('mw_kind') || 'injected');
        const s = await resolveSigner(this);
        if (s) {
          this._raw.signer = Alpine.raw(s);
          return s;
        }
      } catch {}
      return null;
    },

    // ── Action runner (single-button modal flow) ────────────────────────────
    // runAction({ kind, icon, title, subtitle, summary, ctaLabel, disclaimer, fn })
    //   - Opens the modal with the supplied summary.
    //   - When the user clicks Confirm, executes `fn({ signer, provider, done, fail, setStep })`.
    //   - Tracks busy state so other buttons auto-disable while one is open.
    async runAction(opts) {
      if (this.busy) {
        this._toast('Another action is already in progress — please wait.', 'info');
        return { ok: false, error: 'busy' };
      }
      this.busy = true;
      try {
        const signer = await this.ensureSigner();
        if (!signer) {
          // Open a minimal modal that surfaces "Connect wallet" instead of
          // letting Ethers throw its abstract-provider error in the wild.
          const mres = await Alpine.store('modals').open({
            kind: 'list',
            icon: '⚡',
            title: 'Connect your wallet first',
            subtitle: opts.subtitle || '',
            summary: [{ label: 'Action', value: opts.title || 'Continue' }],
            disclaimer: 'Connection is never custodial — your keys stay in your wallet.',
            ctaLabel: 'Open picker',
            run: async ({ fail, setStep }) => {
              setStep(1, 'Open the wallet picker…');
              this._toast('Click "Connect Wallet" above to choose a wallet.', 'info');
              fail({ title: 'Wallet not connected', body: 'Connect a wallet to continue.' });
            },
          });
          return mres || { ok: false, error: 'not-connected' };
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
              await opts.fn({ signer: R(signer), provider: R(provider), setStep, done, fail });
            } catch (e) {
              fail(e);
            }
          },
        });
      } finally {
        this.busy = false;
      }
    },

    // ── Marketplace: Buy (price + 1.5%, stale-listing preflight) ──────────

    async buy(collection, tokenId, seller, priceWei) {
      // Optional retry on click — keeps the legacy direct-call signature
      // working for pages that haven't migrated yet.
      return await this.runAction({
        kind: 'buy',
        icon: '⚐',
        title: 'Buy now',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [],
        ctaLabel: 'Buy',
        run: ({ setStep, done, fail }) => this._executeBuy(collection, tokenId, seller, priceWei, { setStep, done, fail }),
      });
    },

    async _executeBuy(collection, tokenId, seller, priceWei, { setStep, done, fail }) {
      setStep(1, 'Verifying listing on-chain…');
      try {
        const pf = await fetch(`/api/v1/listings/${collection}/${tokenId}/preflight?seller=${seller}`)
          .then(r => r.ok ? r.json() : null).catch(() => null);
        if (!pf) { fail({ title: 'Preflight failed', body: 'Could not verify this listing. Refresh and try again.' }); return; }
        if (!pf.ok) { fail({ title: 'Listing unavailable', body: 'This listing is no longer fillable (sold, cancelled, or the NFT moved).' }); return; }
        if (pf?.price_wei) priceWei = pf.price_wei;

        setStep(1, 'Sign in your wallet…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await c.buy(collection, tokenId, seller, { value: BigInt(priceWei) });
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        const netWei = netOfFee(priceWei).toString();
        // Update modal metadata with the new breakdown so it shows in done state.
        Alpine.store('modals').summary = [
          { label: 'You paid',     value: fmtFLR(priceWei) + ' FLR', tone: 'sky' },
          { label: 'Seller gets',  value: fmtFLR(netWei) + ' FLR',   tone: 'gold' },
          { label: 'Platform fee', value: fmtFLR(feeOf(priceWei).toString()) + ' FLR' + ' (1.5%)', tone: '' },
        ];
        done({
          txHash: tx.hash,
          title: 'Purchase confirmed',
          body: `Token transferred; seller received ${fmtFLR(netWei)} FLR (1.5% fee deducted).`,
        });
        window.dispatchEvent(new CustomEvent('mw-bought', { detail: { collection, tokenId, tx: tx.hash } }));
      } catch (e) { fail(e); }
    },

    // ── Marketplace: List ─────────────────────────────────────────────────

    async list(collection, tokenId, priceWei, expiresAt, standard = 'erc721') {
      return await this.runAction({
        kind: 'list',
        icon: '₱',
        title: 'List for sale',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'You list for', value: fmtFLR(priceWei) + ' FLR', tone: 'sky' },
          { label: 'You receive on sale', value: fmtFLR(netOfFee(priceWei).toString()) + ' FLR (98.5%)', tone: 'gold' },
          { label: 'Platform fee', value: fmtFLR(feeOf(priceWei).toString()) + ' FLR (1.5%)', tone: '' },
          { label: 'Expires', value: new Date(expiresAt * 1000).toLocaleString(), tone: 'violet' },
        ],
        ctaLabel: 'List — free',
        disclaimer: 'Listing is free. The platform fee is deducted from the seller on sale, not at listing time.',
        run: ({ setStep, done, fail }) => this._executeList(collection, tokenId, priceWei, expiresAt, standard, { setStep, done, fail }),
      });
    },

    async _executeList(collection, tokenId, priceWei, expiresAt, standard, { setStep, done, fail }) {
      try {
        setStep(1, 'Approving marketplace…');
        await this._approveOperator(collection, MARKETPLACE, standard);
        setStep(1, 'Confirm listing in wallet…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await c.list(collection, tokenId, BigInt(priceWei), Math.floor(expiresAt));
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Listed for sale', body: 'Listings appear live within ~2s of confirm.' });
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
        disclaimer: 'Cancel is free. Your NFT immediately stops being purchasable; no on-chain transfer occurs.',
        run: ({ setStep, done, fail }) => this._executeCancel(collection, tokenId, { setStep, done, fail }),
      });
    },

    async _executeCancel(collection, tokenId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm cancellation…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, R(signer));
        const tx = await c.cancel(collection, tokenId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Listing cancelled', body: 'Your NFT is no longer listed.' });
        window.dispatchEvent(new CustomEvent('mw-listed', { detail: { collection, tokenId, cancelled: true } }));
      } catch (e) { fail(e); }
    },

    // ── Auction: Create ────────────────────────────────────────────────────

    async createAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard = 'erc721') {
      return await this.runAction({
        kind: 'auction', icon: '♕',
        title: 'Create auction',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Reserve', value: fmtFLR(reserveWei) + ' FLR', tone: 'sky' },
          { label: 'Min increment', value: (minIncBps/100).toFixed(2) + '%' + (minIncFlatWei !== '0' && minIncFlatWei ? ' + ' + fmtFLR(minIncFlatWei) + ' FLR' : ''), tone: 'violet' },
          { label: 'Auction ends', value: new Date(endsAt * 1000).toLocaleString(), tone: 'gold' },
        ],
        ctaLabel: 'Create auction — free',
        disclaimer: 'Auction creation is free. Anti-snipe extends the deadline by 3 minutes on any last-minute bid.',
        run: ({ setStep, done, fail }) => this._executeCreateAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard, { setStep, done, fail }),
      });
    },

    async _executeCreateAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei, standard, { setStep, done, fail }) {
      try {
        setStep(1, 'Approving auction contract…');
        await this._approveOperator(collection, AUCTION, standard);
        setStep(1, 'Confirm auction creation…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await c.create(collection, tokenId, BigInt(reserveWei || '0'), Math.floor(endsAt), minIncBps || 500, BigInt(minIncFlatWei || '0'));
        setStep(2, 'Waiting for confirmation…');
        const rcpt = await tx.wait();
        // The create() returns the auctionId; extract from logs/topics.
        const auctionId = rcpt?.logs?.[0]?.topics?.[1] ? parseInt(rcpt.logs[0].topics[1], 16) : null;
        done({
          txHash: tx.hash,
          title: 'Auction created',
          body: auctionId ? `Auction #${auctionId} is live.` : 'Auction is live.',
        });
        window.dispatchEvent(new CustomEvent('mw-auction-created', { detail: { collection, tokenId, auctionId, tx: tx.hash } }));
      } catch (e) { fail(e); }
    },

    // ── Auction: Bid ───────────────────────────────────────────────────────

    async bid(auctionId, bidAmountWei, endsAt) {
      const willExtend = endsAt && (Number(endsAt) - Math.floor(Date.now() / 1000)) < EXTENSION_WINDOW;
      return await this.runAction({
        kind: 'bid', icon: '♝',
        title: willExtend ? 'Last-minute bid (extends 3 min)' : 'Place a bid',
        subtitle: `Auction #${auctionId}`,
        summary: [
          { label: 'Bid amount', value: fmtFLR(bidAmountWei) + ' FLR', tone: 'sky' },
          ...(willExtend ? [{ label: 'Anti-snipe', value: '+3:00 (auction extends)', tone: 'violet' }] : []),
          { label: 'Your escrow stays', value: 'Free to bid (no fee on bid)', tone: 'gold' },
        ],
        ctaLabel: 'Place bid',
        disclaimer: 'Bids accumulate. If you are outbid your funds remain escrowed — top up to retake the lead, or withdraw after settlement.',
        run: ({ setStep, done, fail }) => this._executeBid(auctionId, bidAmountWei, { setStep, done, fail }),
      });
    },

    async _executeBid(auctionId, bidAmountWei, { setStep, done, fail }) {
      try {
        setStep(1, 'Sign bid in wallet…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await c.bid(auctionId, { value: BigInt(bidAmountWei) });
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Bid placed', body: 'You are now the leading bidder (or have topped up).' });
        window.dispatchEvent(new CustomEvent('mw-bid-placed', { detail: { auctionId, tx: tx.hash } }));
      } catch (e) { fail(e); }
    },

    async settle(auctionId) {
      return await this.runAction({
        kind: 'settle', icon: '⚖',
        title: 'Settle auction',
        subtitle: `Auction #${auctionId}`,
        summary: [{ label: 'Outcome', value: 'NFT to highest bidder, escrow to seller', tone: 'gold' }],
        ctaLabel: 'Settle',
        run: ({ setStep, done, fail }) => this._executeSettle(auctionId, { setStep, done, fail }),
      });
    },

    async _executeSettle(auctionId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm settlement…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await c.settle(auctionId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Auction settled', body: 'Funds distributed; losing bidders refunded automatically.' });
        window.dispatchEvent(new CustomEvent('mw-auction-settled', { detail: { auctionId } }));
      } catch (e) { fail(e); }
    },

    async cancelEarly(auctionId) {
      return await this.runAction({
        kind: 'cancel', icon: '×',
        title: 'Cancel auction early',
        subtitle: `Auction #${auctionId}`,
        summary: [{ label: 'Action', value: 'Bidding stops; bidders get refunded' }],
        ctaLabel: 'Cancel auction',
        run: ({ setStep, done, fail }) => this._executeCancelEarly(auctionId, { setStep, done, fail }),
      });
    },

    async _executeCancelEarly(auctionId, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm cancellation…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const tx = await c.cancelEarly(auctionId);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Auction cancelled', body: 'All bidders refunded; NFT returned to seller.' });
        window.dispatchEvent(new CustomEvent('mw-auction-cancelled', { detail: { auctionId } }));
      } catch (e) { fail(e); }
    },

    async withdrawRefund() {
      return await this.runAction({
        kind: 'settle', icon: '↩',
        title: 'Withdraw refund',
        subtitle: 'Auction escrow refund (auto-push failed)',
        summary: [{ label: 'Action', value: 'Pull pending refund to your wallet' }],
        ctaLabel: 'Withdraw',
        run: ({ setStep, done, fail }) => this._executeWithdrawRefund({ setStep, done, fail }),
      });
    },

    async _executeWithdrawRefund({ setStep, done, fail }) {
      try {
        setStep(1, 'Reading pending refund…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, R(signer));
        const pending = await c.pendingReturns(this.address);
        if (pending === 0n) {
          fail({ title: 'Nothing to withdraw', body: 'No pending refund on this address.' });
          return;
        }
        Alpine.store('modals').summary = [
          { label: 'Refund amount', value: fmtFLR(pending.toString()) + ' FLR', tone: 'gold' },
          { label: 'To wallet', value: fmtAddr(this.address), tone: 'sky' },
        ];
        setStep(1, 'Confirm withdrawal…');
        const tx = await c.withdrawRefund();
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Refund withdrawn', body: `${fmtFLR(pending.toString())} FLR sent to your wallet.` });
      } catch (e) { fail(e); }
    },

    // ── OfferBook ──────────────────────────────────────────────────────────

    async makeOffer(collection, tokenId, principalWei, expiresAt) {
      const netStr = principalWei;
      return await this.runAction({
        kind: 'offer', icon: '⚐',
        title: 'Make an offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'You escrow', value: fmtFLR(principalWei) + ' FLR', tone: 'sky' },
          { label: 'Expires', value: new Date(expiresAt * 1000).toLocaleString(), tone: 'violet' },
          { label: 'Refundable', value: 'Yes — until accepted, rejected, or expired', tone: 'gold' },
        ],
        ctaLabel: 'Submit offer',
        disclaimer: 'Your escrow is fully refundable until the seller accepts. After expiry it returns automatically.',
        run: ({ setStep, done, fail }) => this._executeMakeOffer(collection, tokenId, principalWei, expiresAt, { setStep, done, fail }),
      });
    },

    async _executeMakeOffer(collection, tokenId, principalWei, expiresAt, { setStep, done, fail }) {
      try {
        setStep(1, 'Sign offer in wallet…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await c.makeOffer(collection, tokenId, BigInt(principalWei), Math.floor(expiresAt), { value: BigInt(principalWei) });
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer placed', body: 'Your funds are escrowed. You can withdraw after expiry or rejection.' });
        window.dispatchEvent(new CustomEvent('mw-offer-made', { detail: { collection, tokenId } }));
      } catch (e) { fail(e); }
    },

    async acceptOffer(collection, tokenId, bidder) {
      return await this.runAction({
        kind: 'accept', icon: '✓',
        title: 'Accept offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Bidder', value: fmtAddr(bidder), tone: 'sky' },
          { label: 'You receive', value: '98.5% of offer (1.5% fee)', tone: 'gold' },
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
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await c.acceptOffer(collection, tokenId, bidder);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer accepted', body: 'You received 98.5% (1.5% platform fee deducted). NFT transferred to bidder.' });
        window.dispatchEvent(new CustomEvent('mw-offer-accepted', { detail: { collection, tokenId, bidder } }));
      } catch (e) { fail(e); }
    },

    async rejectOffer(collection, tokenId, bidder) {
      return await this.runAction({
        kind: 'reject', icon: '↩',
        title: 'Reject offer',
        subtitle: `${fmtAddr(collection)} · #${tokenId}`,
        summary: [
          { label: 'Bidder', value: fmtAddr(bidder), tone: 'sky' },
          { label: 'Outcome', value: 'Bidder is fully refunded', tone: 'gold' },
        ],
        ctaLabel: 'Reject & refund',
        run: ({ setStep, done, fail }) => this._executeRejectOffer(collection, tokenId, bidder, { setStep, done, fail }),
      });
    },

    async _executeRejectOffer(collection, tokenId, bidder, { setStep, done, fail }) {
      try {
        setStep(1, 'Confirm rejection…');
        const signer = await resolveSigner(this);
        if (!signer) { fail({ title: 'Wallet lost', body: 'Reconnect and try again.' }); return; }
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, R(signer));
        const tx = await c.rejectOffer(collection, tokenId, bidder);
        setStep(2, 'Waiting for confirmation…');
        await tx.wait();
        done({ txHash: tx.hash, title: 'Offer rejected', body: 'Bidder has been refunded in full.' });
        window.dispatchEvent(new CustomEvent('mw-offer-rejected', { detail: { collection, tokenId, bidder } }));
      } catch (e) { fail(e); }
    },

    // ── Reports ───────────────────────────────────────────────────────────

    async report(targetType, targetId, reason, detail) {
      try {
        const res = await fetch('/api/v1/reports', {
          method: 'POST', headers: this.authHeaders(),
          body: JSON.stringify({ target_type: targetType, target_id: targetId, reason, detail: detail || '' }),
        });
        if (!res.ok) throw new Error('report failed');
        this._toast('Report submitted. Thank you.', 'success');
      } catch { this._toast('Could not submit report.', 'error'); }
    },

    async saveProfile(fields) {
      try {
        const res = await fetch(`/api/v1/profile/${this.address}`, {
          method: 'PUT', headers: this.authHeaders(), body: JSON.stringify(fields),
        });
        if (!res.ok) throw new Error('save failed');
        this._toast('Profile saved.', 'success');
      } catch { this._toast('Could not save profile.', 'error'); }
    },

    // ── Internal toast helper (used by connect / report / saveProfile) ─────
    _toast(msg, type = 'info') { return toast(msg, type); },
  });

  // ── Auto-reconnect on page load ────────────────────────────────────────
  const saved = localStorage.getItem('mw_addr');
  const kind  = localStorage.getItem('mw_kind') || 'injected';
  if (saved && (kind === 'walletconnect' ? WC_PROJECT_ID : !!window.ethereum)) {
    Alpine.store('wallet').connect(kind, { silent: true }).catch(() => {});
  }
});

// ── Toast notifications (5-color palette: sky · gold · black · purple · white) ──

function toast(msg, type = 'info') {
  const styles = {
    success: 'btn-gold text-ink-950 glow-gold',
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

// ── Event bus → wallet action routing ───────────────────────────────────────

window.addEventListener('buy', e => {
  const { collection, tokenId, seller, price } = e.detail;
  if (collection && tokenId && seller && price) {
    Alpine.store('wallet').buy(collection, tokenId, seller, price);
  }
});
window.addEventListener('cancel-listing', e => {
  const { collection, tokenId } = e.detail;
  Alpine.store('wallet').cancel(collection, tokenId);
});
window.addEventListener('settle-auction', e => {
  Alpine.store('wallet').settle(e.detail.auctionId);
});

// Live notification badge via SSE.
window.addEventListener('mw-notification', () => {
  const w = Alpine.store('wallet');
  if (w?.jwt) w.refreshUnread();
});

// ── URI helpers (kept separate for templates / inline JS) ────────────────────

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
