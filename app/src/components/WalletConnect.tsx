'use client';

import { useEffect, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { WagmiProvider } from 'wagmi';
import { createAppKit } from '@reown/appkit/react';
import { WagmiAdapter } from '@reown/appkit-adapter-wagmi';
import { useAppKit, useAppKitAccount, useAppKitNetwork } from '@reown/appkit/react';
import { http } from 'wagmi';

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

function WalletButton() {
  const { open } = useAppKit();
  const { address, isConnected, status } = useAppKitAccount();
  const { chainId, switchNetwork } = useAppKitNetwork();

  const connecting = status === 'connecting' || status === 'reconnecting';
  const wrongNetwork = isConnected && chainId !== undefined && chainId !== flareCoston2.id;

  const displayAddr = address ? `${address.slice(0, 6)}...${address.slice(-4)}` : '';
  const copyAddress = () => { if (address) navigator.clipboard.writeText(address).catch(() => {}); };

  const wasConnectedRef = useRef(false);
  useEffect(() => {
    if (isConnected && address) {
      wasConnectedRef.current = true;
      try {
        localStorage.setItem('mw_addr', address.toLowerCase());
        localStorage.setItem('mw_kind', 'walletconnect');
      } catch (_) {}
    } else if (wasConnectedRef.current && !isConnected) {
      wasConnectedRef.current = false;
      try {
        localStorage.removeItem('mw_addr');
        localStorage.removeItem('mw_kind');
      } catch (_) {}
    }
  }, [isConnected, address]);

  // Expose globals so the mobile menu / external triggers can open AppKit
  useEffect(() => {
    if (typeof window !== 'undefined') {
      (window as any).__MW_APPKIT_OPEN__ = () => { if (!isConnected && !connecting) open(); };
      (window as any).__MW_APPKIT_DISCONNECT__ = () => { open({ view: 'Account' }); };
      (window as any).__MW_APPKIT_READY__ = true;
    }
    return () => {
      if (typeof window !== 'undefined') {
        try { delete (window as any).__MW_APPKIT_OPEN__; } catch (_) {}
        try { delete (window as any).__MW_APPKIT_DISCONNECT__; } catch (_) {}
        try { delete (window as any).__MW_APPKIT_READY__; } catch (_) {}
      }
    };
  }, [isConnected, connecting, open]);

  if (!isConnected) {
    return (
      <button
        onClick={() => open()}
        disabled={connecting}
        style={{
          padding: '0.625rem 1.25rem',
          borderRadius: '0.75rem',
          background: connecting
            ? 'linear-gradient(135deg, rgba(125,211,252,0.25), rgba(14,165,233,0.25))'
            : 'linear-gradient(135deg, #a78bfa, #7c3aed)',
          color: connecting ? 'rgba(255,255,255,0.4)' : '#ffffff',
          fontWeight: 800,
          fontSize: '0.8125rem',
          border: 'none',
          cursor: connecting ? 'not-allowed' : 'pointer',
          display: 'inline-flex',
          alignItems: 'center',
          gap: '0.5rem',
          transition: 'all 0.2s ease',
          boxShadow: connecting ? 'none' : '0 0 22px -6px rgba(167,139,250,0.45), 0 4px 12px -4px rgba(124,58,237,0.3)',
          fontFamily: 'inherit',
          lineHeight: 1,
        }}
        onMouseEnter={(e) => {
          if (!connecting) {
            e.currentTarget.style.opacity = '0.92';
            e.currentTarget.style.transform = 'scale(1.02)';
            e.currentTarget.style.boxShadow = '0 0 30px -4px rgba(167,139,250,0.6), 0 6px 20px -6px rgba(124,58,237,0.35)';
          }
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.opacity = '1';
          e.currentTarget.style.transform = 'scale(1)';
          e.currentTarget.style.boxShadow = '0 0 22px -6px rgba(167,139,250,0.45), 0 4px 12px -4px rgba(124,58,237,0.3)';
        }}
        onMouseDown={(e) => {
          if (!connecting) e.currentTarget.style.transform = 'scale(0.97)';
        }}
        onMouseUp={(e) => {
          if (!connecting) e.currentTarget.style.transform = 'scale(1.02)';
        }}
      >
        {connecting ? (
          <>
            <span style={{ display: 'inline-block', width: '1rem', height: '1rem', border: '2px solid rgba(255,255,255,0.2)', borderTopColor: '#a78bfa', borderRadius: '50%', animation: 'spin 0.8s linear infinite' }} />
            Connecting…
          </>
        ) : (
          <>
            <span style={{ fontSize: '1rem', lineHeight: 1, color: '#ddd6fe' }}>⌬</span>
            <span>Connect Wallet</span>
          </>
        )}
      </button>
    );
  }

  if (wrongNetwork) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', padding: '0.5rem 0.75rem', borderRadius: '0.75rem', background: 'rgba(251,191,36,0.1)', border: '1px solid rgba(251,191,36,0.3)', boxShadow: '0 0 18px -4px rgba(251,191,36,0.45)' }}>
        <span style={{ fontSize: '0.625rem', fontWeight: 700, color: '#fde68a', textTransform: 'uppercase', letterSpacing: '0.05em' }}>⚠ Wrong Network</span>
        <button
          onClick={() => switchNetwork(flareCoston2.id)}
          style={{
            padding: '0.375rem 0.75rem',
            borderRadius: '0.5rem',
            background: 'linear-gradient(135deg, #fcd34d, #f59e0b)',
            color: '#09090b',
            fontWeight: 700,
            fontSize: '0.6875rem',
            border: 'none',
            cursor: 'pointer',
            fontFamily: 'inherit',
            boxShadow: '0 0 14px -3px rgba(251,191,36,0.45)',
          }}
        >
          Switch to Coston2
        </button>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', padding: '0.375rem 0.5rem 0.375rem 0.75rem', borderRadius: '0.75rem', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)', transition: 'all 0.2s' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.375rem' }}>
        <span style={{ display: 'inline-block', width: '0.5rem', height: '0.5rem', borderRadius: '50%', background: '#7dd3fc', boxShadow: '0 0 8px rgba(125,211,252,0.5)', position: 'relative' }}>
          <span style={{ position: 'absolute', inset: '-3px', borderRadius: '50%', background: 'rgba(125,211,252,0.2)', animation: 'pulse-ring 1.5s ease-out infinite' }} />
        </span>
        <span style={{ fontSize: '0.5625rem', fontWeight: 700, color: 'rgba(255,255,255,0.35)', textTransform: 'uppercase', letterSpacing: '0.1em' }}>Wallet</span>
        <span style={{ fontSize: '0.5rem', padding: '0.125rem 0.375rem', borderRadius: '0.25rem', background: 'rgba(167,139,250,0.2)', color: '#ddd6fe', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.05em', border: '1px solid rgba(167,139,250,0.25)' }}>WC</span>
        <span
          onClick={copyAddress}
          title="Click to copy"
          style={{
            fontSize: '0.75rem',
            fontWeight: 700,
            color: '#fafafa',
            fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
            cursor: 'pointer',
            transition: 'color 0.2s',
          }}
          onMouseEnter={(e) => { e.currentTarget.style.color = '#fcd34d'; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = '#fafafa'; }}
        >
          {displayAddr}
        </span>
      </div>
      <button
        onClick={() => open({ view: 'Account' })}
        style={{
          padding: '0.25rem 0.625rem',
          borderRadius: '0.5rem',
          background: 'transparent',
          border: '1px solid rgba(255,255,255,0.08)',
          color: 'rgba(255,255,255,0.4)',
          fontSize: '0.6875rem',
          fontWeight: 600,
          cursor: 'pointer',
          fontFamily: 'inherit',
          transition: 'all 0.2s',
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.color = '#fca5a5';
          e.currentTarget.style.borderColor = 'rgba(252,165,165,0.3)';
          e.currentTarget.style.background = 'rgba(239,68,68,0.08)';
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.color = 'rgba(255,255,255,0.4)';
          e.currentTarget.style.borderColor = 'rgba(255,255,255,0.08)';
          e.currentTarget.style.background = 'transparent';
        }}
      >
        Disconnect
      </button>
    </div>
  );
}

let _wagmiConfig: any = null;
let _appKitReady = false;

async function initAppKit(): Promise<void> {
  if (typeof window === 'undefined') return;
  if (_appKitReady) return;
  const projectId = getProjectId();
  if (!projectId) { console.warn('[mw-wc] No Reown project ID'); return; }
  try {
    const adapter = new WagmiAdapter({ networks: chains, projectId, transports });
    _wagmiConfig = adapter.wagmiConfig;
    createAppKit({
      adapters: [adapter], networks: chains as any, defaultNetwork: flareCoston2, projectId,
      metadata: { name: 'MagicWebb', description: 'NFT Marketplace on Flare Network', url: 'https://magicwebb.fly.dev', icons: ['/favicon.ico'] },
      features: { analytics: false, email: false, socials: false },
      themeMode: 'dark', enableWalletSelector: true, enableNetworkSelector: true,
    });
    _appKitReady = true;
  } catch (e) { console.error('[mw-wc] AppKit init failed:', e); }
}

const queryClient = new QueryClient();

export default function WalletConnect() {
  const [ready, setReady] = useState(false);
  useEffect(() => { initAppKit().then(() => setReady(true)); }, []);

  if (!ready || !_wagmiConfig) {
    return (
      <button disabled style={{ padding: '0.625rem 1.25rem', borderRadius: '0.75rem', background: 'linear-gradient(135deg, rgba(167,139,250,0.15), rgba(124,58,237,0.1))', border: '1px solid rgba(255,255,255,0.05)', color: 'rgba(255,255,255,0.2)', fontWeight: 800, fontSize: '0.8125rem', cursor: 'default', display: 'inline-flex', alignItems: 'center', gap: '0.5rem', fontFamily: 'inherit', animation: 'shimmer-placeholder 1.5s ease-in-out infinite' }}>
        <span style={{ fontSize: '1rem', opacity: 0.3 }}>⌬</span><span>Loading…</span>
      </button>
    );
  }

  return (
    <WagmiProvider config={_wagmiConfig}>
      <QueryClientProvider client={queryClient}>
        <WalletButton />
      </QueryClientProvider>
    </WagmiProvider>
  );
}
