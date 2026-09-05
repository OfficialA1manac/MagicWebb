// Single source for the docs registry (spec B4 "Docs"). Both the docs index
// page and DocLayout's sidebar render from this list, so a new document is
// added exactly once and appears everywhere in the same order.
//
// Underscore prefix keeps Astro from treating this file as a route.

export interface DocEntry {
  slug: string;
  title: string;
  icon: string;
  accent: string;
  blurb: string;
}

export const DOCS: DocEntry[] = [
  { slug: 'start-here',  title: 'Start Here',           icon: '🚀', accent: '#7dd3fc',
    blurb: 'The 2-minute guide: connect, get test FLR, buy or list your first NFT.' },
  { slug: 'whitepaper',  title: 'Whitepaper',           icon: '📜', accent: '#34d399',
    blurb: 'Vision, market, and the seller-pays economic model.' },
  { slug: 'technical',   title: 'Technical Whitepaper', icon: '⚙️', accent: '#7dd3fc',
    blurb: 'Contracts, escrow flows, and the indexer pipeline in depth.' },
  { slug: 'user-guide',  title: 'User Guide',           icon: '🧭', accent: '#a78bfa',
    blurb: 'Listing, bidding, offers, and withdrawals — step by step.' },
  { slug: 'capabilities', title: 'What You Can Do',     icon: '✅', accent: '#4ade80',
    blurb: 'Viewer, buyer, seller: every action for listings, auctions, offers and refunds.' },
  { slug: 'faq',         title: 'FAQ',                  icon: '💡', accent: '#fcd34d',
    blurb: 'Quick answers: fees, refunds, wallets, and safety.' },
  { slug: 'token-hooks', title: 'Token Architecture',   icon: '🔗', accent: '#fb7185',
    blurb: 'Future token integration points anchored in the manager contract.' },
  { slug: 'api',         title: 'API Reference',        icon: '🔌', accent: '#22d3ee',
    blurb: 'Complete REST API documentation — endpoints, auth, schemas, and examples.' },
];
