// MagicWebb wallet — Alpine.js store + ethers.js contract interactions.
// Chain: Coston2 testnet (chainId 114 = 0x72). Native FLR only.

const CHAIN_ID = 114;
const RPC_URL  = 'https://coston2-api.flare.network/ext/C/rpc';

// Contract addresses (Coston2 testnet) — overwrite from window.MW_ADDRS if set.
let MARKETPLACE = (window.MW_ADDRS && window.MW_ADDRS.MARKETPLACE) || '0x0000000000000000000000000000000000000000';
let AUCTION     = (window.MW_ADDRS && window.MW_ADDRS.AUCTION)     || '0x0000000000000000000000000000000000000000';
let OFFERBOOK   = (window.MW_ADDRS && window.MW_ADDRS.OFFERBOOK)   || '0x0000000000000000000000000000000000000000';

// Platform fee: 1.5% = 150 bps (paid by taker on top of price/bid/offer).
const FEE_BPS = 150n;
function withFee(amountWei) { return amountWei + (amountWei * FEE_BPS) / 10000n; }

// ── ABIs (minimal, matching the reworked contracts) ──────────────────────────

const MARKETPLACE_ABI = [
  // Listing is FREE. msg.value must be 0.
  'function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external',
  'function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external',
  'function cancel(address coll, uint256 id) external',
  // Buyer pays price + 1.5% fee. seller param disambiguates ERC-1155 stacks.
  'function buy(address coll, uint256 id, address seller) external payable',
];

const AUCTION_ABI = [
  'function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint128 sellerFlatMinFLR) external returns (uint256)',
  'function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint128 sellerFlatMinFLR) external returns (uint256)',
  // Bidder pays bid + 1.5% fee. Outbid refunds bid only.
  'function bid(uint256 id, uint128 bidAmount) external payable',
  'function settle(uint256 id) external',
  'function cancelEarly(uint256 id) external',
  'function withdrawRefund() external',
];

const OFFERBOOK_ABI = [
  'function markOfferEligible(address coll, uint256 tokenId) external',
  'function removeOfferEligible(address coll, uint256 tokenId) external',
  // Bidder pays offer + 1.5% fee. Stacked: subsequent calls compound principal.
  'function makeOffer(address coll, uint256 tokenId, uint128 offerAmount, uint64 duration) external payable',
  'function refundExpired(address coll, uint256 tokenId, address bidder) external',
  // Seller pays no fee (fees were collected up-front).
  'function acceptOffer(address coll, uint256 tokenId, address bidder) external',
];

const ERC721_ABI = [
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
  'function getApproved(uint256 tokenId) external view returns (address)',
  'function ownerOf(uint256 tokenId) external view returns (address)',
];

// Plain-English mapping of contract revert errors → user-facing toast.
const REVERT_MAP = {
  NotOwner:        "You don't own this NFT.",
  NotListed:       "This listing is no longer active.",
  WrongPrice:      "The price changed. Refresh and try again.",
  Expired:         "This listing has expired.",
  NotApproved:     "Approve the marketplace contract first.",
  BidTooLow:       "Bid must beat the current high by at least 5%.",
  AuctionEnded:    "This auction has already ended.",
  AuctionLive:     "This auction hasn't ended yet.",
  WrongBidValue:   "Send your bid plus the 1.5% fee.",
  BelowMinPrice:   "Amount is below the 0.01 FLR floor.",
  InvalidExpiry:   "Listing expiry must be in the future and within 90 days.",
  InvalidWindow:   "Auction must end within 7 days.",
  InvalidDuration: "Offer duration must be 14 days or less.",
  NoPosition:      "No offer position to act on.",
  PositionExpired: "This offer position has already expired.",
  PositionLive:    "This offer position hasn't expired yet.",
  NotSeller:       "Only the seller can do that.",
  NotActive:       "This auction or position is no longer active.",
  TransferFailed:  "Token transfer failed — the NFT may have moved.",
  WithdrawFailed:  "Payout failed.",
};
function explain(e) {
  const text = (e && (e.reason || e.shortMessage || e.message)) || 'Transaction failed';
  const m = text.match(/([A-Z][A-Za-z]+)\(\)/);
  if (m && REVERT_MAP[m[1]]) return REVERT_MAP[m[1]];
  if (text.includes('user rejected')) return 'You rejected the transaction.';
  return text;
}

// ── Alpine store ─────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('wallet', {
    provider: null,
    signer:   null,
    address:  null,
    chainId:  null,
    method:   null, // 'metamask' | 'walletconnect'
    jwt:      localStorage.getItem('mw_jwt') || null,

    get shortAddr() {
      return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : '';
    },
    get connected() { return !!this.address; },

    // ── Connect ──────────────────────────────────────────────────────────────

    async connect(method = 'auto') {
      try {
        if (method === 'walletconnect' || (method === 'auto' && !window.ethereum)) {
          await this._connectWalletConnect();
        } else {
          await this._connectInjected();
        }
        if (this.address) {
          localStorage.setItem('mw_addr', this.address);
          localStorage.setItem('mw_method', this.method);
          await this._authenticate();
          toast('Wallet connected', 'success');
        }
      } catch (e) {
        toast(explain(e), 'error');
      }
    },

    async _connectInjected() {
      if (!window.ethereum) throw new Error('No wallet detected. Install MetaMask or use WalletConnect.');
      const provider = new ethers.BrowserProvider(window.ethereum);
      const accounts = await provider.send('eth_requestAccounts', []);
      const network  = await provider.getNetwork();
      if (Number(network.chainId) !== CHAIN_ID) await this._switchChain();
      this.provider = provider;
      this.signer   = await provider.getSigner();
      this.address  = accounts[0].toLowerCase();
      this.chainId  = Number(network.chainId);
      this.method   = 'metamask';
    },

    async _connectWalletConnect() {
      if (!window.EthereumProvider) throw new Error('WalletConnect not loaded');
      const projectId = window.WC_PROJECT_ID;
      if (!projectId) throw new Error('WalletConnect projectId is not configured');
      const wc = await window.EthereumProvider.init({
        projectId,
        chains: [CHAIN_ID],
        rpcMap: { [CHAIN_ID]: RPC_URL },
        showQrModal: true,
      });
      await wc.connect();
      const provider = new ethers.BrowserProvider(wc);
      const accounts = await provider.send('eth_accounts', []);
      this.provider = provider;
      this.signer   = await provider.getSigner();
      this.address  = (accounts[0] || '').toLowerCase();
      this.chainId  = CHAIN_ID;
      this.method   = 'walletconnect';
    },

    async _switchChain() {
      try {
        await window.ethereum.request({
          method: 'wallet_switchEthereumChain',
          params: [{ chainId: '0x72' }],
        });
      } catch (e) {
        if (e.code === 4902) {
          await window.ethereum.request({
            method: 'wallet_addEthereumChain',
            params: [{
              chainId: '0x72',
              chainName: 'Coston2',
              nativeCurrency: { name: 'C2FLR', symbol: 'C2FLR', decimals: 18 },
              rpcUrls: [RPC_URL],
            }],
          });
        } else { throw e; }
      }
    },

    disconnect() {
      this.provider = null;
      this.signer   = null;
      this.address  = null;
      this.chainId  = null;
      this.method   = null;
      this.jwt      = null;
      localStorage.removeItem('mw_addr');
      localStorage.removeItem('mw_method');
      localStorage.removeItem('mw_jwt');
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
      return this.jwt ? { Authorization: 'Bearer ' + this.jwt } : {};
    },

    // ── Approvals ────────────────────────────────────────────────────────────

    async _approveOperator(collection, operator) {
      const c = new ethers.Contract(collection, ERC721_ABI, this.signer);
      const approved = await c.isApprovedForAll(this.address, operator);
      if (!approved) {
        toast('Approving contract…', 'info');
        const tx = await c.setApprovalForAll(operator, true);
        await tx.wait();
        toast('Approved', 'success');
      }
    },

    // ── Marketplace ──────────────────────────────────────────────────────────

    async buy(collection, tokenId, seller, priceWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        // Preflight to catch stale listings before sending value
        const pf = await this.preflightBuy(collection, tokenId);
        if (pf && pf.stale) {
          toast(pf.reason || 'This listing is stale — the NFT moved.', 'error');
          return;
        }
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        const total = withFee(BigInt(priceWei));
        toast('Sending buy…', 'info');
        const tx = await c.buy(collection, tokenId, seller, { value: total });
        await tx.wait();
        toast('Purchase confirmed', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async preflightBuy(collection, tokenId) {
      try {
        const r = await fetch(`/api/v1/listings/${collection}/${tokenId}/preflight`);
        if (!r.ok) return null;
        return await r.json();
      } catch (_) { return null; }
    },

    async list(collection, tokenId, priceWei, expiresAt) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, MARKETPLACE);
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Creating listing…', 'info');
        const tx = await c.list(collection, tokenId, BigInt(priceWei), Math.floor(expiresAt));
        await tx.wait();
        toast('Listed — no fee was charged', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async cancel(collection, tokenId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Cancelling…', 'info');
        const tx = await c.cancel(collection, tokenId);
        await tx.wait();
        toast('Listing cancelled', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    // ── AuctionHouse ─────────────────────────────────────────────────────────

    async createAuction(collection, tokenId, reserveWei, endsAt, sellerFlatMinWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, AUCTION);
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Creating auction…', 'info');
        const tx = await c.create(
          collection, tokenId,
          BigInt(reserveWei || '0'),
          Math.floor(endsAt),
          BigInt(sellerFlatMinWei || '0'),
        );
        await tx.wait();
        toast('Auction created', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async bid(auctionId, bidAmountWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        const total = withFee(BigInt(bidAmountWei));
        toast('Placing bid (bid + 1.5% fee)…', 'info');
        const tx = await c.bid(auctionId, BigInt(bidAmountWei), { value: total });
        await tx.wait();
        toast('Bid placed', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async settle(auctionId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Settling…', 'info');
        const tx = await c.settle(auctionId);
        await tx.wait();
        toast('Auction settled', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async cancelAuction(auctionId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Cancelling auction…', 'info');
        const tx = await c.cancelEarly(auctionId);
        await tx.wait();
        toast('Auction cancelled — current bid refunded (fee retained)', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async withdrawAuctionRefund() {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        const tx = await c.withdrawRefund();
        await tx.wait();
        toast('Refund withdrawn', 'success');
      } catch (e) { toast(explain(e), 'error'); }
    },

    // ── OfferBook ────────────────────────────────────────────────────────────

    async markOfferEligible(collection, tokenId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        const tx = await c.markOfferEligible(collection, tokenId);
        await tx.wait();
        toast('Offers enabled on this NFT', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async makeOffer(collection, tokenId, offerWei, durationSec) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        const total = withFee(BigInt(offerWei));
        toast('Submitting offer (offer + 1.5% fee, locked until accept/expiry)…', 'info');
        const tx = await c.makeOffer(collection, tokenId, BigInt(offerWei), durationSec || 0, { value: total });
        await tx.wait();
        toast('Offer placed', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },

    async acceptOffer(collection, tokenId, bidder) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, OFFERBOOK);
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Accepting offer (no fee)…', 'info');
        const tx = await c.acceptOffer(collection, tokenId, bidder);
        await tx.wait();
        toast('Offer accepted', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(explain(e), 'error'); }
    },
  });

  // Notifications store
  Alpine.store('notifs', {
    items: [],
    unread: 0,
    open: false,
    async refresh() {
      const w = Alpine.store('wallet');
      if (!w.jwt) return;
      try {
        const r = await fetch('/api/v1/notifications?limit=50', { headers: w.authHeaders() });
        if (!r.ok) return;
        const data = await r.json();
        this.items = data.items || [];
        this.unread = data.unread || 0;
      } catch (_) {}
    },
    async markRead() {
      const w = Alpine.store('wallet');
      if (!w.jwt) return;
      await fetch('/api/v1/notifications/read', { method: 'POST', headers: w.authHeaders() });
      this.unread = 0;
      this.items = this.items.map(i => ({ ...i, read_at: new Date().toISOString() }));
    },
    toggle() {
      this.open = !this.open;
      if (this.open) this.markRead();
    },
  });

  // NFT picker (drives the "+" buttons on listings/auctions/offers pages)
  Alpine.store('picker', {
    open: false,
    mode: null, // 'listing' | 'auction' | 'offer'
    nfts: [],
    loading: false,
    selected: null,
    async show(mode) {
      const w = Alpine.store('wallet');
      if (!w.address) { await w.connect(); if (!w.address) return; }
      this.mode = mode;
      this.open = true;
      this.loading = true;
      try {
        const r = await fetch(`/api/v1/wallet/${w.address}/nfts`);
        this.nfts = r.ok ? await r.json() : [];
      } finally { this.loading = false; }
    },
    close() { this.open = false; this.selected = null; },
    select(nft) { this.selected = nft; },
  });

  // Auto-reconnect if previously connected
  const saved = localStorage.getItem('mw_addr');
  const method = localStorage.getItem('mw_method') || 'auto';
  if (saved) {
    Alpine.store('wallet').connect(method).catch(() => {});
  }
  setTimeout(() => Alpine.store('notifs').refresh(), 500);
});

// ── SSE notification listener ────────────────────────────────────────────────

window.addEventListener('htmx:sseMessage', e => {
  if (e.detail && e.detail.type === 'notification') {
    Alpine.store('notifs').refresh();
  }
});

// ── Toast notifications ──────────────────────────────────────────────────────

function toast(msg, type = 'info') {
  const colors = {
    success: 'bg-emerald-600',
    error:   'bg-red-600',
    info:    'bg-neutral-700',
  };
  const el = document.createElement('div');
  el.className = `pointer-events-auto px-4 py-3 rounded-xl text-white text-sm font-medium shadow-xl transition-opacity duration-300 ${colors[type] || colors.info}`;
  el.textContent = msg;
  document.getElementById('toasts')?.appendChild(el);
  setTimeout(() => { el.style.opacity = '0'; }, 3000);
  setTimeout(() => el.remove(), 3400);
}

// ── Event-bus aliases ────────────────────────────────────────────────────────

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
