// MagicWebb wallet — Alpine.js store + ethers.js contract interactions
// Chain: Coston2 testnet (chainId 114 = 0x72)
const CHAIN_ID = 114;
const RPC_URL   = 'https://coston2-api.flare.network/ext/C/rpc';

// Contract addresses (Coston2 testnet)
const MARKETPLACE = '0xec47a481513da81ff59a6c4002a98803039994e5';
const AUCTION     = '0xf62e931d807f87ebd90cc3254b0a34a76c326331';
const OFFERBOOK   = '0x7e88e86f61e6ad80abd828b6bcedaa86311736f0';

// Platform fee: 1.5% = 150 bps
const FEE_BPS = 150n;

// ── ABIs (minimal, matching deployed contracts) ───────────────────────────────

const MARKETPLACE_ABI = [
  // list(coll, id, price, expiresAt) payable — msg.value = price * 150 / 10000
  'function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external payable',
  // cancel(coll, id) — seller only
  'function cancel(address coll, uint256 id) external',
  // buy(coll, id) payable — msg.value = price
  'function buy(address coll, uint256 id) external payable',
];

const AUCTION_ABI = [
  // create(coll, tokenId, reserve, endsAt, minIncBps) — needs approval first
  'function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps) external returns (uint256)',
  // bid(id, bidAmount) payable — msg.value = bidAmount + bidAmount*150/10000
  'function bid(uint256 id, uint128 bidAmount) external payable',
  // settle(id) — anyone can call after endsAt
  'function settle(uint256 id) external',
  // cancelEarly(id) — seller only, before endsAt
  'function cancelEarly(uint256 id) external',
];

const OFFERBOOK_ABI = [
  // markEligible(coll, tokenId) — owner opts token in to receive offers
  'function markEligible(address coll, uint256 tokenId) external',
  // removeEligible(coll, tokenId) — owner opts out
  'function removeEligible(address coll, uint256 tokenId) external',
  // makeOffer(coll, tokenId) payable — bidder deposits ETH as offer
  'function makeOffer(address coll, uint256 tokenId) external payable',
  // withdrawOffer(coll, tokenId) — bidder reclaims ETH
  'function withdrawOffer(address coll, uint256 tokenId) external',
  // acceptOffer(coll, tokenId, bidder) — NFT owner accepts a specific offer
  'function acceptOffer(address coll, uint256 tokenId, address bidder) external',
  // eligible(coll, tokenId) view — returns address that marked eligible (0 = not eligible)
  'function eligible(address, uint256) external view returns (address)',
  // offers(coll, tokenId, bidder) view — returns offer amount in wei (0 = no offer)
  'function offers(address, uint256, address) external view returns (uint256)',
];

const ERC721_ABI = [
  'function approve(address to, uint256 tokenId) external',
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
  'function getApproved(uint256 tokenId) external view returns (address)',
  'function ownerOf(uint256 tokenId) external view returns (address)',
];

// ── Alpine store ───────────────────────────────────────────────────────────────

document.addEventListener('alpine:init', () => {
  Alpine.store('wallet', {
    provider: null,
    signer:   null,
    address:  null,
    chainId:  null,
    jwt:      localStorage.getItem('mw_jwt') || null,

    get shortAddr() {
      return this.address ? this.address.slice(0, 6) + '…' + this.address.slice(-4) : '';
    },
    get connected() { return !!this.address; },

    // ── Connect ────────────────────────────────────────────────────────────

    async connect() {
      if (!window.ethereum) {
        toast('No wallet detected. Install MetaMask.', 'error');
        return;
      }
      try {
        const provider = new ethers.BrowserProvider(window.ethereum);
        const accounts = await provider.send('eth_requestAccounts', []);
        const network  = await provider.getNetwork();
        if (Number(network.chainId) !== CHAIN_ID) await this._switchChain();
        this.provider = provider;
        this.signer   = await provider.getSigner();
        this.address  = accounts[0].toLowerCase();
        this.chainId  = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        // SIWE auth for signed actions
        await this._authenticate();
        toast('Wallet connected', 'success');
      } catch (e) {
        toast(e.message || 'Connection failed', 'error');
      }
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
        // Auth failure is non-fatal — read-only actions still work
        console.warn('SIWE auth failed:', e);
      }
    },

    // ── Approvals ──────────────────────────────────────────────────────────

    async _approveOperator(collection, operator) {
      const c = new ethers.Contract(collection, ERC721_ABI, this.signer);
      const approved = await c.isApprovedForAll(this.address, operator);
      if (!approved) {
        toast('Approving contract…', 'info');
        const tx = await c.setApprovalForAll(operator, true);
        await tx.wait();
        toast('Approved!', 'success');
      }
    },

    // ── Marketplace: Buy ───────────────────────────────────────────────────

    async buy(collection, tokenId, priceWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Sending buy transaction…', 'info');
        const tx = await c.buy(collection, tokenId, { value: BigInt(priceWei) });
        toast('Transaction submitted', 'info');
        await tx.wait();
        toast('Purchase confirmed!', 'success');
      } catch (e) { toast(e.reason || e.message || 'Transaction failed', 'error'); }
    },

    // ── Marketplace: List ──────────────────────────────────────────────────
    // list fee = price * 150 / 10000 must be sent as msg.value

    async list(collection, tokenId, priceWei, expiresAt) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, MARKETPLACE);
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        const priceBig = BigInt(priceWei);
        const listFee  = priceBig * FEE_BPS / 10000n;
        toast('Creating listing…', 'info');
        const tx = await c.list(collection, tokenId, priceBig, Math.floor(expiresAt), { value: listFee });
        await tx.wait();
        toast('Listed successfully!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Listing failed', 'error'); }
    },

    // ── Marketplace: Cancel ────────────────────────────────────────────────

    async cancel(collection, tokenId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Cancelling listing…', 'info');
        const tx = await c.cancel(collection, tokenId);
        await tx.wait();
        toast('Listing cancelled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Cancel failed', 'error'); }
    },

    // ── AuctionHouse: Create ───────────────────────────────────────────────

    async createAuction(collection, tokenId, reserveWei, endsAt, minIncBps) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, AUCTION);
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Creating auction…', 'info');
        const tx = await c.create(
          collection, tokenId,
          BigInt(reserveWei || '0'),
          Math.floor(endsAt),
          minIncBps || 500,
        );
        await tx.wait();
        toast('Auction created!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Create auction failed', 'error'); }
    },

    // ── AuctionHouse: Bid ──────────────────────────────────────────────────
    // msg.value = bidAmount + bidAmount * 150 / 10000

    async bid(auctionId, bidAmountWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        const bidBig = BigInt(bidAmountWei);
        const fee    = bidBig * FEE_BPS / 10000n;
        const total  = bidBig + fee;
        toast('Placing bid…', 'info');
        const tx = await c.bid(auctionId, bidBig, { value: total });
        await tx.wait();
        toast('Bid placed!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Bid failed', 'error'); }
    },

    // ── AuctionHouse: Settle ───────────────────────────────────────────────

    async settle(auctionId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Settling auction…', 'info');
        const tx = await c.settle(auctionId);
        await tx.wait();
        toast('Auction settled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Settle failed', 'error'); }
    },

    // ── AuctionHouse: Cancel Early ─────────────────────────────────────────

    async cancelEarly(auctionId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Cancelling auction…', 'info');
        const tx = await c.cancelEarly(auctionId);
        await tx.wait();
        toast('Auction cancelled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Cancel failed', 'error'); }
    },

    // ── OfferBook: Mark Eligible ───────────────────────────────────────────

    async markEligible(collection, tokenId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Enabling offers…', 'info');
        const tx = await c.markEligible(collection, tokenId);
        await tx.wait();
        toast('Offers enabled!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Enable offers failed', 'error'); }
    },

    // ── OfferBook: Make Offer ──────────────────────────────────────────────
    // Deposits ETH on-chain. Token must be marked eligible by owner.

    async makeOffer(collection, tokenId, amountWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Submitting offer…', 'info');
        const tx = await c.makeOffer(collection, tokenId, { value: BigInt(amountWei) });
        await tx.wait();
        toast('Offer placed!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Offer failed', 'error'); }
    },

    // ── OfferBook: Withdraw Offer ──────────────────────────────────────────

    async withdrawOffer(collection, tokenId) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Withdrawing offer…', 'info');
        const tx = await c.withdrawOffer(collection, tokenId);
        await tx.wait();
        toast('Offer withdrawn!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Withdraw failed', 'error'); }
    },

    // ── OfferBook: Accept Offer ────────────────────────────────────────────
    // NFT owner calls this. Must have approved OFFERBOOK first.

    async acceptOffer(collection, tokenId, bidder) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        await this._approveOperator(collection, OFFERBOOK);
        const c = new ethers.Contract(OFFERBOOK, OFFERBOOK_ABI, this.signer);
        toast('Accepting offer…', 'info');
        const tx = await c.acceptOffer(collection, tokenId, bidder);
        await tx.wait();
        toast('Offer accepted!', 'success');
        setTimeout(() => location.reload(), 1200);
      } catch (e) { toast(e.reason || e.message || 'Accept failed', 'error'); }
    },
  });

  // Auto-reconnect if previously connected
  const saved = localStorage.getItem('mw_addr');
  if (saved && window.ethereum) {
    Alpine.store('wallet').connect().catch(() => {});
  }
});

// Convenience alias for x-data="walletStore()" pattern
function walletStore() { return Alpine.store('wallet'); }

// ── Toast notifications ───────────────────────────────────────────────────────

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

// ── Event bus listeners ────────────────────────────────────────────────────────

window.addEventListener('buy', e => {
  const { collection, tokenId, price } = e.detail;
  Alpine.store('wallet').buy(collection, tokenId, price);
});

window.addEventListener('cancel-listing', e => {
  const { collection, tokenId } = e.detail;
  Alpine.store('wallet').cancel(collection, tokenId);
});

window.addEventListener('settle-auction', e => {
  const { auctionId } = e.detail;
  Alpine.store('wallet').settle(auctionId);
});
