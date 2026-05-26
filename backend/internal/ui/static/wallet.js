// MagicWebb wallet — Alpine.js store + ethers.js contract interactions
const CHAIN_ID = 114; // Coston2
const RPC_URL  = 'https://coston2-api.flare.network/ext/C/rpc';
const MARKETPLACE = '0xec47a481513da81ff59a6c4002a98803039994e5';
const AUCTION     = '0xf62e931d807f87ebd90cc3254b0a34a76c326331';
const OFFERBOOK   = '0x7e88e86f61e6ad80abd828b6bcedaa86311736f0';

// Minimal ABIs for contract interactions
const MARKETPLACE_ABI = [
  'function buy(address coll, uint256 id) external payable',
  'function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external',
  'function cancel(address coll, uint256 id) external',
];
const AUCTION_ABI = [
  'function commitBid(uint256 auctionId) external payable',
  'function settle(uint256 auctionId) external',
];
const OFFERBOOK_ABI = [
  'function acceptOffer(address coll, uint256 tokenId, address bidder, uint128 amount, uint64 nonce, bytes calldata sig) external',
];
const ERC721_ABI = [
  'function approve(address to, uint256 tokenId) external',
  'function setApprovalForAll(address op, bool approved) external',
  'function isApprovedForAll(address owner, address op) external view returns (bool)',
];

document.addEventListener('alpine:init', () => {
  Alpine.store('wallet', {
    provider: null, signer: null, address: null, chainId: null,

    get shortAddr() {
      return this.address ? this.address.slice(0,6) + '…' + this.address.slice(-4) : '';
    },
    get connected() { return !!this.address; },

    async connect() {
      if (!window.ethereum) { toast('No wallet detected. Install MetaMask.', 'error'); return; }
      try {
        const provider = new ethers.BrowserProvider(window.ethereum);
        const accounts = await provider.send('eth_requestAccounts', []);
        const network  = await provider.getNetwork();
        if (Number(network.chainId) !== CHAIN_ID) await this.switchChain();
        this.provider = provider;
        this.signer   = await provider.getSigner();
        this.address  = accounts[0].toLowerCase();
        this.chainId  = Number(network.chainId);
        localStorage.setItem('mw_addr', this.address);
        toast('Wallet connected', 'success');
      } catch(e) { toast(e.message || 'Connection failed', 'error'); }
    },

    async switchChain() {
      await window.ethereum.request({
        method: 'wallet_addEthereumChain',
        params: [{ chainId: '0x72', chainName: 'Coston2', nativeCurrency: { name: 'FLR', symbol: 'C2FLR', decimals: 18 }, rpcUrls: [RPC_URL] }],
      });
    },

    async buy(collection, tokenId, priceWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(MARKETPLACE, MARKETPLACE_ABI, this.signer);
        toast('Sending buy transaction…', 'info');
        const tx = await c.buy(collection, tokenId, { value: BigInt(priceWei) });
        toast('Transaction submitted', 'info');
        await tx.wait();
        toast('Purchase confirmed!', 'success');
      } catch(e) { toast(e.reason || e.message || 'Transaction failed', 'error'); }
    },

    async approveAll(collection) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      const c = new ethers.Contract(collection, ERC721_ABI, this.signer);
      const approved = await c.isApprovedForAll(this.address, MARKETPLACE);
      if (!approved) {
        toast('Approving marketplace…', 'info');
        const tx = await c.setApprovalForAll(MARKETPLACE, true);
        await tx.wait();
        toast('Approved!', 'success');
      }
    },

    async bid(auctionId, amountWei) {
      if (!this.signer) { await this.connect(); if (!this.signer) return; }
      try {
        const c = new ethers.Contract(AUCTION, AUCTION_ABI, this.signer);
        toast('Placing bid…', 'info');
        const tx = await c.commitBid(auctionId, { value: BigInt(amountWei) });
        await tx.wait();
        toast('Bid placed!', 'success');
      } catch(e) { toast(e.reason || e.message || 'Bid failed', 'error'); }
    },
  });

  // auto-reconnect on page load
  const saved = localStorage.getItem('mw_addr');
  if (saved && window.ethereum) {
    Alpine.store('wallet').connect().catch(() => {});
  }
});

// Alpine shortcut alias
function walletStore() { return Alpine.store('wallet'); }

// ── Toast notifications ───────────────────────────────────────────────────────
function toast(msg, type = 'info') {
  const colors = { success: 'bg-emerald-600', error: 'bg-red-600', info: 'bg-neutral-700' };
  const el = document.createElement('div');
  el.className = `pointer-events-auto px-4 py-3 rounded-xl text-white text-sm font-medium shadow-xl transition-all ${colors[type] || colors.info}`;
  el.textContent = msg;
  document.getElementById('toasts')?.appendChild(el);
  setTimeout(() => el.classList.add('opacity-0'), 3000);
  setTimeout(() => el.remove(), 3400);
}

// ── Buy event listener (dispatched from listing cards) ────────────────────────
window.addEventListener('buy', e => {
  const { collection, tokenId, price } = e.detail;
  Alpine.store('wallet').buy(collection, tokenId, price);
});
