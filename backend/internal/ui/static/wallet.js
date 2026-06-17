// MagicWebb wallet — Alpine.js store + ethers.js contract interactions
// Chain: Coston2 testnet (chainId 114 = 0x72). Seller-pays 1.5% fee model.
const CHAIN_ID = 114;
const RPC_URL  = 'https://coston2-api.flare.network/ext/C/rpc';

// Contract addresses — injected by the layout from server config (single
// source of truth: backend/.env). Never hardcode: a redeploy would silently
// point every wallet action at dead contracts.
const MARKETPLACE = window.MW_MARKETPLACE || '';
const AUCTION     = window.MW_AUCTION || '';
const OFFERBOOK   = window.MW_OFFERBOOK || '';
if (!MARKETPLACE || !AUCTION || !OFFERBOOK) {
  console.error('MagicWebb: contract addresses not injected — wallet actions disabled.');
}

// Platform fee: 1.5% = 150 bps. Charged on a successful sale, deducted from the seller.
// Buyers, bidders and offerers send exactly their amount — no fee on top.
const FEE_BPS = 150n;
function feeOf(wei)    { const a = BigInt(wei); return (a * FEE_BPS) / 10000n; }
function netOfFee(wei) { const a = BigInt(wei); return a - feeOf(a); }

// WalletConnect v2 project id (https://cloud.walletconnect.com). Optional.
const WC_PROJECT_ID = window.MW_WC_PROJECT_ID || '';

// Anti-snipe window mirrored from AuctionHouse.EXTENSION_WINDOW (3 minutes).
const EXTENSION_WINDOW = 180;

// ── ABIs (minimal, matching the seller-pays contracts) ─────────────────────────

const MARKETPLACE_ABI = [
  'function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external',
  'function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external',
  'function cancel(address coll, uint256 id) external',
  // buy: msg.value = price + 1.5%; seller selects which listing to fill.
  'function buy(address coll, uint256 id, address seller) external payable',
];

const AUCTION_ABI = [
  'function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat) external returns (uint256)',
  'function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat) external returns (uint256)',
  // bid: msg.value escrows and ADDS to your cumulative total (free; no refund
  // on outbid — top up to retake the lead; losers auto-refunded after settle).
  'function bid(uint256 id) external payable',
  'function settle(uint256 id) external',
  'function cancelEarly(uint256 id) external',
  // Pull-fallback: only needed when an automatic push refund failed.
  'function withdrawRefund() external',
  'function pendingReturns(address) external view returns (uint256)',
];

const OFFERBOOK_ABI = [
  // makeOffer: msg.value = principal (free; fully refundable). Positions stack.
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

// ── Plain-English revert mapping ───────────────────────────────────────────────

function revertMessage(e) {
  if (e && (e.code === 4001 || e.code === 'ACTION_REJECTED')) return 'You rejected the request.';
  const raw = [e?.reason, e?.shortMessage, e?.info?.error?.message, e?.data?.message, e?.message]
    .filter(Boolean).join(' ');
  const map = [
    ['WrongValue',     "Amount sent must equal the offer amount exactly."],
    ['WrongBidValue',  "Amount sent must equal the bid amount exactly."],
    ['WrongPrice',     "Amount sent must equal the listing price exactly."],
    ['BelowMinPrice',  'Minimum is 0.01 FLR.'],
    ['BidTooLow',      'Your bid is below the minimum increment.'],
    ['NotApproved',    'Approve the contract to manage this NFT first.'],
    ['NotOwner',       "You don't hold this NFT."],
    ['NotSeller',      'Only the seller can do that.'],
    ['Expired',        'This listing or offer has expired.'],
    ['InvalidExpiry',  'Pick an expiry within the allowed window.'],
    ['AuctionEnded',   'This auction has already ended.'],
    ['AuctionLive',    'This auction is still live.'],
    ['OfferActive',    "This offer hasn't expired yet."],
    ['NoOffer',        'No active offer found.'],
    ['InvalidWindow',  'Duration is outside the allowed range.'],
    ['EntriesHalted',  'The marketplace is temporarily paused for new activity — settlements, refunds and withdrawals still work.'],
    ['NotActive',      'This auction is not active.'],
    ['NotSettled',     'This auction has not settled yet.'],
    ['BidOverflow',    'Bid total exceeds the supported maximum.'],
    ['NothingToWithdraw', 'No pending refund to withdraw.'],
    ['insufficient funds', 'Not enough FLR to cover the amount plus gas.'],
  ];
  for (const [needle, msg] of map) {
    if (raw.toLowerCase().includes(needle.toLowerCase())) return msg;
  }
  if (/revert|CALL_EXCEPTION/i.test(raw)) {
    return 'Transaction reverted — the item may have just sold or changed. Refresh and retry.';
  }
  return raw || 'Transaction failed.';
}

// ── Alpine store ───────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('wallet', {
    provider: null,
    signer:   null,
    address:  null,
    chainId:  null,
    jwt:      localStorage.getItem('mw_jwt') || null,
    unread:   0,

    get shortAddr() {
      return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : '';
    },
    get connected() { return !!this.address; },

    // ── Connect (MetaMask | WalletConnect v2) ─────────────────────────────

    // silent=true: background re-attach on page load — no toasts. Navigating
    // between pages is not "connecting" again; only an explicit click on
    // Connect should announce itself.
    async connect(kind = 'injected', { silent = false } = {}) {
      try {
        let eip1193;
        if (kind === 'walletconnect') {
          eip1193 = await this._wcProvider();
        } else {
          if (!window.ethereum) { if (!silent) toast('No wallet detected. Install MetaMask or use WalletConnect.', 'error'); return; }
          eip1193 = window.ethereum;
        }
        const provider = new ethers.BrowserProvider(eip1193);
        const accounts = await provider.send('eth_requestAccounts', []);
        const network  = await provider.getNetwork();
        if (Number(network.chainId) !== CHAIN_ID && kind === 'injected') await this._switchChain();
        this.provider = provider;
        this.signer   = await provider.getSigner();
        this.address  = accounts[0].toLowerCase();
        this.chainId  = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        localStorage.setItem('mw_kind', kind);
        await this._authenticate();
        await this.refreshUnread();
        if (!silent) toast('Wallet connected', 'success');
      } catch (e) {
        if (!silent) toast(revertMessage(e), 'error');
      }
    },

    async _wcProvider() {
      if (!WC_PROJECT_ID) throw new Error('WalletConnect not configured');
      const { EthereumProvider } = await import('https://esm.sh/@walletconnect/ethereum-provider@2.11.2');
      const wc = await EthereumProvider.init({
        projectId: WC_PROJECT_ID,
        chains: [CHAIN_ID],
        rpcMap: { [CHAIN_ID]: RPC_URL },
        showQrModal: true,
      });
      await wc.connect();
      return wc;
    },

    disconnect() {
      this.provider = this.signer = this.address = null;
      this.jwt = null; this.unread = 0;
      localStorage.removeItem('mw_addr'); localStorage.removeItem('mw_jwt'); localStorage.removeItem('mw_kind');
    },

    async _switchChain() {
      await window.ethereum.request({
        method: 'wallet_addEthereumChain',
        params: [{
          chainId: '0x72',
          chainName: 'Coston2',
          nativeCurrency: { name: 'C2FLR', symbol: 'C2FLR', decimals: 18 },
          rpcUrls: [RPC_URL],
        }],
      });
    },

    async _authenticate() {
      try {
        const nonceRes = await fetch(`/auth/nonce?address=${this.address}`);
        if (!nonceRes.ok) return;
        const { nonce } = await nonceRes.json();
        const message = `Sign in to MagicWebb\nAddress: ${this.address}\nNonce: ${nonce}`;
        const sig = await this.signer.signMessage(message);
        const verifyRes = await fetch('/auth/verify', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ address: this.address, message, signature: sig }),
        });
        if (!verifyRes.ok) return;
        const { token } = await verifyRes.json();
        this.jwt = token;
        localStorage.setItem('mw_jwt', token);
      } catch (e) {
        console.warn('SIWE auth failed:', e);
      }
    },

    authHeaders() {
      return this.jwt ? { 'Authorization': 'Bearer ' + this.jwt, 'Content-Type': 'application/json' }
                      : { 'Content-Type': 'application/json' };
    },

    // ── Notifications ─────────────────────────────────────────────────────

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

    // ── Approvals ─────────────────────────────────────────────────────────

    async _approveOperator(collection, operator) {
      const c = new ethers.Contract(collection, ERC721_ABI, this.signer);
      if (!(await c.isApprovedForAll(this.address, operator))) {
        toast('Approving contract…', 'info');
        const tx = await c.setApprovalForAll(operator, true);
        await tx.wait();
        toast('Approved!', 'success');
      }
    },

    async _ensure() {
      if (!this.signer) { await this.connect(localStorage.getItem('mw_kind') || 'injected'); }
      return !!this.signer;
    },

    // ── Marketplace: Buy (price + 1.5%, with stale-listing preflight) ─────

    async buy(collection, tokenId, seller, priceWei) {
      if (!(await this._ensure())) return;
      try {
        const pf = await fetch(`/api/v1/listings/${collection}/${tokenId}/preflight?seller=${seller}`)
          .then(r => r.ok ? r.json() : null).catch(() => null);
        if (!pf) {
          toast('Could not verify this listing. Refresh and try again.', 'error');
          return;
        }
        if (!pf.ok) {
          toast('This listing is no longer fillable (sold, cancelled, or the NFT moved).', 'error');
          return;
        }
        if (pf && pf.price_wei) priceWei = pf.price_wei;
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Sending buy (you pay the listed price)…', 'info');
        const tx = await c.buy(collection, tokenId, seller, { value: BigInt(priceWei) });
        await tx.wait();
        toast('Purchase confirmed!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── Marketplace: List (FREE — no fee at listing) ──────────────────────

    async list(collection, tokenId, priceWei, expiresAt) {
      if (!(await this._ensure())) return;
      try {
        await this._approveOperator(collection, MARKETPLACE);
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Creating listing (free)…', 'info');
        const tx = await c.list(collection, tokenId, BigInt(priceWei), Math.floor(expiresAt));
        await tx.wait();
        toast('Listed successfully!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    async cancel(collection, tokenId) {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Cancelling listing…', 'info');
        const tx = await c.cancel(collection, tokenId);
        await tx.wait();
        toast('Listing cancelled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── AuctionHouse: Create (with flat minimum increment) ────────────────

    async createAuction(collection, tokenId, reserveWei, endsAt, minIncBps, minIncFlatWei) {
      if (!(await this._ensure())) return;
      try {
        await this._approveOperator(collection, AUCTION);
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Creating auction (free)…', 'info');
        const tx = await c.create(
          collection, tokenId,
          BigInt(reserveWei || '0'),
          Math.floor(endsAt),
          minIncBps || 500,
          BigInt(minIncFlatWei || '0'),
        );
        await tx.wait();
        toast('Auction created!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── AuctionHouse: Bid (bid + 1.5%, anti-snipe aware) ──────────────────

    async bid(auctionId, bidAmountWei, endsAt) {
      if (!(await this._ensure())) return;
      try {
        if (endsAt && (Number(endsAt) - Math.floor(Date.now() / 1000)) < EXTENSION_WINDOW) {
          toast('Last-minute bid — this extends the auction by 3 minutes (anti-snipe).', 'info');
        }
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Placing bid (free — you pay only your bid)…', 'info');
        const tx = await c.bid(auctionId, { value: BigInt(bidAmountWei) });
        await tx.wait();
        toast('Bid placed!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    async settle(auctionId) {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Settling auction…', 'info');
        const tx = await c.settle(auctionId);
        await tx.wait();
        toast('Auction settled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    async cancelEarly(auctionId) {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Cancelling auction…', 'info');
        const tx = await c.cancelEarly(auctionId);
        await tx.wait();
        toast('Auction cancelled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── AuctionHouse: pull-fallback withdrawal ──────────────────────────────
    // Refunds are pushed automatically by the keeper; this path only matters
    // when a push to the bidder's address failed (e.g. a contract wallet that
    // rejected plain transfers). Checks the balance first to avoid a pointless
    // reverting tx.
    async withdrawRefund() {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        const pending = await c.pendingReturns(this.address);
        if (pending === 0n) { toast('No pending refund to withdraw.', 'info'); return; }
        toast('Withdrawing pending refund…', 'info');
        const tx = await c.withdrawRefund();
        await tx.wait();
        toast('Refund withdrawn!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── OfferBook: Make Offer (free; full principal escrowed, stacked) ─────

    async makeOffer(collection, tokenId, principalWei, expiresAt) {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Submitting offer (free — fully refundable)…', 'info');
        const tx = await c.makeOffer(collection, tokenId, BigInt(principalWei), Math.floor(expiresAt), { value: BigInt(principalWei) });
        await tx.wait();
        toast('Offer placed! (locked until accept / reject / expiry)', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── OfferBook: Accept one position ────────────────────────────────────

    async acceptOffer(collection, tokenId, bidder) {
      if (!(await this._ensure())) return;
      try {
        await this._approveOperator(collection, OFFERBOOK);
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Accepting offer…', 'info');
        const tx = await c.acceptOffer(collection, tokenId, bidder);
        await tx.wait();
        toast('Offer accepted! Seller receives 98.5% (1.5% platform fee).', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── OfferBook: Reject a bidder's position (full principal refunded) ────

    async rejectOffer(collection, tokenId, bidder) {
      if (!(await this._ensure())) return;
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Rejecting offer…', 'info');
        const tx = await c.rejectOffer(collection, tokenId, bidder);
        await tx.wait();
        toast('Offer rejected — principal refunded to bidder.', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(revertMessage(e), 'error'); }
    },

    // ── Reports ───────────────────────────────────────────────────────────

    async report(targetType, targetId, reason, detail) {
      if (!(await this._ensure())) return;
      try {
        const res = await fetch('/api/v1/reports', {
          method: 'POST', headers: this.authHeaders(),
          body: JSON.stringify({ target_type: targetType, target_id: targetId, reason, detail: detail || '' }),
        });
        if (!res.ok) throw new Error('report failed');
        toast('Report submitted. Thank you.', 'success');
      } catch (e) { toast('Could not submit report.', 'error'); }
    },

    async saveProfile(fields) {
      if (!(await this._ensure())) return;
      try {
        const res = await fetch(`/api/v1/profile/${this.address}`, {
          method: 'PUT', headers: this.authHeaders(), body: JSON.stringify(fields),
        });
        if (!res.ok) throw new Error('save failed');
        toast('Profile saved.', 'success');
        setTimeout(() => location.reload(), 800);
      } catch (e) { toast('Could not save profile.', 'error'); }
    },
  });

  // Auto-reconnect if previously connected — silent: no "Wallet connected"
  // toast replay on every page navigation.
  const saved = localStorage.getItem('mw_addr');
  const kind  = localStorage.getItem('mw_kind') || 'injected';
  if (saved && (window.ethereum || kind === 'walletconnect')) {
    Alpine.store('wallet').connect(kind, { silent: true }).catch(() => {});
  }
});

function walletStore() { return Alpine.store('wallet'); }

function isBareIPFSCID(uri) {
  return (uri.startsWith('Qm') && uri.length >= 44) || (uri.startsWith('baf') && uri.length >= 59);
}

function resolveURI(uri) {
  uri = (uri || '').trim();
  if (!uri || uri.startsWith('data:')) return uri;
  if (uri.startsWith('ipfs://ipfs/')) return 'https://cloudflare-ipfs.com/ipfs/' + uri.slice(13);
  if (uri.startsWith('ipfs://')) return 'https://cloudflare-ipfs.com/ipfs/' + uri.slice(7);
  if (isBareIPFSCID(uri)) return 'https://cloudflare-ipfs.com/ipfs/' + uri;
  if (uri.startsWith('ar://')) return 'https://arweave.net/' + uri.slice(5);
  return uri;
}

function mediaURL(uri) {
  if (!uri || uri.startsWith('data:') || uri.startsWith('/')) return uri;
  if (uri.startsWith('http://') || uri.startsWith('https://')) {
    return '/api/v1/media?url=' + encodeURIComponent(uri);
  }
  const resolved = resolveURI(uri);
  if (resolved.startsWith('http://') || resolved.startsWith('https://')) {
    return '/api/v1/media?url=' + encodeURIComponent(resolved);
  }
  return uri;
}

// ── Toast notifications ───────────────────────────────────────────────────────

function toast(msg, type = 'info') {
  const colors = { success: 'bg-emerald-600', error: 'bg-red-600', info: 'bg-neutral-700' };
  const el = document.createElement('div');
  el.className = `pointer-events-auto px-4 py-3 rounded-xl text-white text-sm font-medium shadow-xl transition-opacity duration-300 ${colors[type] || colors.info}`;
  el.textContent = msg;
  document.getElementById('toasts')?.appendChild(el);
  setTimeout(() => { el.style.opacity = '0'; }, 3600);
  setTimeout(() => el.remove(), 4000);
}

// ── Event bus listeners ────────────────────────────────────────────────────────

window.addEventListener('buy', e => {
  const { collection, tokenId, seller, price } = e.detail;
  Alpine.store('wallet').buy(collection, tokenId, seller, price);
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
