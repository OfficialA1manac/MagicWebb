'use client';

import { useEffect, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { WagmiProvider } from 'wagmi';
import { createAppKit } from '@reown/appkit/react';
import { WagmiAdapter } from '@reown/appkit-adapter-wagmi';
import { useAppKit, useAppKitAccount, useAppKitNetwork } from '@reown/appkit/react';
import { http } from 'wagmi';

// ── Flare chains ──────────────────────────────────────────────────────────────
const flareCoston2 = {
  id: 114,
  name: 'Flare Coston2',
  nativeCurrency: { name: 'Coston2 Flare', symbol: 'C2FLR', decimals: 18 },
  rpcUrls: { default: { http: ['https://coston2-api.flare.network/ext/C/rpc'] } },
  blockExplorers: { default: { name: 'Coston2 Explorer', url: 'https://coston2-explorer.flare.network' } },
} as const;

const flareNetwork = {
  id: 14,
  name: 'Flare',
  nativeCurrency: { name: 'Flare', symbol: 'FLR', decimals: 18 },
  rpcUrls: { default: { http: ['https://flare-api.flare.network/ext/C/rpc'] } },
  blockExplorers: { default: { name: 'Flare Explorer', url: 'https://flare-explorer.flare.network' } },
} as const;

const chains = [flareCoston2, flareNetwork];
const transports = {
  [flareCoston2.id]: http('https://coston2-api.flare.network/ext/C/rpc'),
  [flareNetwork.id]: http('https://flare-api.flare.network/ext/C/rpc'),
};

function getProjectId(): string {
  return (import.meta.env.PUBLIC_REOWN_PROJECT_ID as string) || '';
}

// ── Inner component (calls AppKit hooks — only rendered when providers exist) ──
function WalletButton() {
  const { open } = useAppKit();
  const { address, isConnected, status } = useAppKitAccount();
  const { chainId, switchNetwork } = useAppKitNetwork();

  const connecting = status === 'connecting' || status === 'reconnecting';
  const wrongNetwork = isConnected && chainId !== undefined && chainId !== flareCoston2.id;

  const displayAddr = address ? `${address.slice(0, 6)}...${address.slice(-4)}` : '';
  const copyAddress = () => { if (address) navigator.clipboard.writeText(address).catch(() => {}); };

  // Sync wallet state to localStorage so the Go HTMX pages (/, /auctions,
  // /token/:addr/:id, etc.) can read the connected address from
  // localStorage.mw_addr / localStorage.mw_kind and show the saved-wallet pill
  // or connected state. Without this bridge, navigating from an Astro page to
  // a Go HTMX page shows the user as disconnected.
  //
  // useRef guard prevents wiping localStorage on initial mount (before wagmi
  // restores the session): only clear localStorage on an explicit disconnect
  // transition (was connected → now disconnected), never on first render.
  const wasConnectedRef = useRef(false);
  useEffect(() => {
    if (isConnected && address) {
      wasConnectedRef.current = true;
      try {
        localStorage.setItem('mw_addr', address.toLowerCase());
        localStorage.setItem('mw_kind', 'walletconnect');
      } catch (_) {}
    } else if (wasConnectedRef.current && !isConnected) {
      // Only clear on explicit disconnect (transition from connected → disconnected)
      wasConnectedRef.current = false;
      try {
        localStorage.removeItem('mw_addr');
        localStorage.removeItem('mw_kind');
      } catch (_) {}
    }
    // Intentionally ignore the initial-render case (!wasConnected && !isConnected)
    // to preserve any address saved by a previous session.
  }, [isConnected, address]);

  if (!isConnected) {
    return (
      <button className="connect-btn" onClick={() => open()} disabled={connecting}>
        {connecting ? (
          <><span className="spinner" /> Connecting…</>
        ) : (
          <>
            <svg className="wallet-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <rect x="2" y="6" width="20" height="12" rx="2" />
              <circle cx="16" cy="12" r="2" fill="currentColor" />
            </svg>
            Connect Wallet
          </>
        )}
      </button>
    );
  }

  if (wrongNetwork) {
    return (
      <div className="network-warning">
        <p>Wrong network. Switch to <strong>Flare Coston2</strong>.</p>
        <button className="switch-btn" onClick={() => switchNetwork(flareCoston2.id)}>
          Switch Network
        </button>
      </div>
    );
  }

  return (
    <div className="connected-state">
      <div className="wallet-badge">
        <span className="status-dot" />
        <span className="address-display" onClick={copyAddress} title="Click to copy">
          {displayAddr}
        </span>
      </div>
      <button className="disconnect-btn" onClick={() => open({ view: 'Account' })}>
        Disconnect
      </button>
    </div>
  );
}

// ── Outer component — initialises AppKit once, wraps children in providers ────
let _wagmiConfig: any = null;
let _appKitReady = false;

async function initAppKit(): Promise<void> {
  if (typeof window === 'undefined') return;
  if (_appKitReady) return;

  const projectId = getProjectId();
  if (!projectId) {
    console.warn('[mw-wc] No Reown project ID — set PUBLIC_REOWN_PROJECT_ID in .env');
    return;
  }

  try {
    const adapter = new WagmiAdapter({ networks: chains, projectId, transports });
    _wagmiConfig = adapter.wagmiConfig;

    createAppKit({
      adapters: [adapter],
      networks: chains as any,
      defaultNetwork: flareCoston2,
      projectId,
      metadata: {
        name: 'MagicWebb',
        description: 'NFT Marketplace on Flare Network',
        url: 'https://magicwebb.fly.dev',
        icons: ['/favicon.ico'],
      },
      features: { analytics: true, email: false, socials: false },
      themeMode: 'dark',
      enableWalletSelector: true,
      enableNetworkSelector: true,
    });

    _appKitReady = true;
  } catch (e) {
    console.error('[mw-wc] AppKit init failed:', e);
  }
}

const queryClient = new QueryClient();

export default function WalletConnect() {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    initAppKit().then(() => setReady(true));
  }, []);

  // Not ready yet — show a minimal placeholder so the navbar doesn't collapse
  if (!ready || !_wagmiConfig) {
    return <button className="connect-btn" disabled style={{ opacity: 0.5 }}>•••</button>;
  }

  return (
    <WagmiProvider config={_wagmiConfig}>
      <QueryClientProvider client={queryClient}>
        <WalletButton />
      </QueryClientProvider>
    </WagmiProvider>
  );
}
