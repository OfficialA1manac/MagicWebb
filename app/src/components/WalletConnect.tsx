'use client';

import { useCallback, useEffect, useRef, useState, type MutableRefObject } from 'react';

// ── Lazy wallet stack ────────────────────────────────────────────────────────
// AppKit + wagmi + react-query are ~1.9 MB of JS. They load on demand:
//   • at mount when the page needs the wallet (`main[data-mw-needs-wallet]`,
//     set by BaseLayout for /token /auction(s) /offers /profile),
//   • on the first click of Connect / Reconnect anywhere else,
//   • when page chrome calls window.__MW_APPKIT_OPEN__ before the stack is up
//     (NFTGrid "Buy", profile empty state) — the shim loads, then opens.
// The reconnect pill (address from localStorage) renders without any of it.
type Mods = {
  wagmi: typeof import('wagmi');
  appkit: typeof import('@reown/appkit/react');
  adapter: typeof import('@reown/appkit-adapter-wagmi');
  rq: typeof import('@tanstack/react-query');
};
let _mods: Mods | null = null;
let _modsP: Promise<Mods> | null = null;
function loadMods(): Promise<Mods> {
  if (_mods) return Promise.resolve(_mods);
  if (!_modsP) {
    _modsP = Promise.all([
      import('wagmi'),
      import('@reown/appkit/react'),
      import('@reown/appkit-adapter-wagmi'),
      import('@tanstack/react-query'),
    ]).then(([wagmi, appkit, adapter, rq]) => (_mods = { wagmi, appkit, adapter, rq }));
  }
  return _modsP;
}

// Target chain is derived from server-injected window globals so the same
// build works for Coston2 (114), Songbird (19) and Flare (14). Falls back to
// Coston2 defaults if the globals are absent (dev mode).
function getTargetChain() {
  if (typeof window === 'undefined') {
    return { id: 114, name: 'Flare Coston2', nativeCurrency: { name: 'Coston2 Flare', symbol: 'C2FLR', decimals: 18 }, rpcUrls: { default: { http: ['https://coston2-api.flare.network/ext/C/rpc'] } }, blockExplorers: { default: { name: 'Coston2 Explorer', url: 'https://coston2-explorer.flare.network' } } };
  }
  const chainId = Number(window.MW_CHAIN_ID || '114');
  const rpcUrl = window.MW_RPC_URL || 'https://coston2-api.flare.network/ext/C/rpc';
  const name = window.MW_NETWORK_NAME || 'Flare Coston2';
  const currency = window.MW_NATIVE_CURRENCY || 'C2FLR';
  const explorer = window.MW_EXPLORER || 'https://coston2-explorer.flare.network';
  return {
    id: chainId,
    name,
    nativeCurrency: { name, symbol: currency, decimals: 18 },
    rpcUrls: { default: { http: [rpcUrl] } },
    blockExplorers: { default: { name: name + ' Explorer', url: explorer } },
  };
}

const targetChain = getTargetChain();
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- AppKitNetwork type differs between library versions; cast once at the boundary
const targetAppKitNetwork = targetChain as any;
const chains = [targetChain];

function getProjectId(): string {
  let id = (import.meta.env.PUBLIC_REOWN_PROJECT_ID as string) || '';
  if (!id && typeof window !== 'undefined') id = window.MW_WC_PROJECT_ID || '';
  return id;
}

function readStoredAddr(): string | null {
  try {
    const a = localStorage.getItem('mw_addr');
    return a && a.length === 42 && a.startsWith('0x') ? a : null;
  } catch { return null; }
}
function forgetStoredAddr() {
  try { localStorage.removeItem('mw_addr'); localStorage.removeItem('mw_kind'); } catch { /* ignore */ }
}
const short = (a: string) => `${a.slice(0, 6)}…${a.slice(-4)}`;
const explorerAddr = (a: string) => `${(typeof window !== 'undefined' && window.MW_EXPLORER) || targetChain.blockExplorers.default.url}/address/${a}`;

// ── Light shell pieces (no wallet libs) ──────────────────────────────────────
function ConnectButton({ onClick, busy, label }: { onClick: () => void; busy?: boolean; label?: string }) {
  return (
    <button type="button" className="btn btn-primary wc-connect" onClick={onClick} disabled={busy} aria-busy={busy || undefined}>
      {busy ? (
        <><span className="wc-spin" aria-hidden="true" />{label || 'Loading…'}</>
      ) : (
        <><span className="wc-connect-full">Connect wallet</span><span className="wc-connect-short">Connect</span></>
      )}
    </button>
  );
}

function ReconnectPill({ addr, onReconnect, onForget, busy }: { addr: string; onReconnect: () => void; onForget: () => void; busy?: boolean }) {
  return (
    <div className="wc-pill">
      <button type="button" className="btn btn-secondary btn-sm" onClick={onReconnect} disabled={busy}>
        <span className="wc-dot" aria-hidden="true" />{busy ? 'Reconnecting…' : 'Reconnect'}
      </button>
      <span className="mono wc-addr">{short(addr)}</span>
      <button type="button" className="icon-btn wc-forget" onClick={onForget} title="Forget wallet" aria-label="Forget wallet">✕</button>
    </div>
  );
}

// ── Connected pill with menu (needs the wallet libs) ─────────────────────────
function WalletButton({ wantOpen }: { wantOpen: MutableRefObject<boolean> }) {
  const m = _mods!;
  const { open } = m.appkit.useAppKit();
  const { disconnect } = m.wagmi.useDisconnect();
  const { address, isConnected, status } = m.appkit.useAppKitAccount();
  const { chainId } = m.appkit.useAppKitNetwork();

  const connecting = (status as string) === 'connecting' || (status as string) === 'reconnecting' || (status as string) === 'initializing';
  const wrongNetwork = isConnected && chainId !== undefined && chainId !== targetChain.id;

  const [storedAddr, setStoredAddr] = useState<string | null>(() => readStoredAddr());
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // Open the modal once if the user asked for it before the stack loaded.
  useEffect(() => {
    if (wantOpen.current && !isConnected && !connecting) { wantOpen.current = false; open(); }
  }, [wantOpen, isConnected, connecting, open]);

  const wasConnectedRef = useRef(false);
  const prevAddressRef = useRef<string | null>(null);
  useEffect(() => {
    if (isConnected && address) {
      wasConnectedRef.current = true;
      const addrLower = address.toLowerCase();
      // Only persist + notify when the address actually changed: wagmi's
      // hydration (initializing→reconnecting→connected) must not re-render
      // the profile page on every load.
      if (prevAddressRef.current !== addrLower) {
        prevAddressRef.current = addrLower;
        try { localStorage.setItem('mw_addr', addrLower); localStorage.setItem('mw_kind', 'walletconnect'); } catch { /* ignore */ }
        setStoredAddr(addrLower);
        window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
      }
    } else if (wasConnectedRef.current && !isConnected) {
      wasConnectedRef.current = false;
      prevAddressRef.current = null;
      forgetStoredAddr();
      setStoredAddr(null);
      window.dispatchEvent(new CustomEvent('mw-wallet-changed'));
    }
  }, [isConnected, address]);

  // Real globals replace the shell's shim once the stack is live.
  useEffect(() => {
    window.__MW_APPKIT_OPEN__ = () => { if (!isConnected && !connecting) open(); };
    window.__MW_APPKIT_DISCONNECT__ = () => { disconnect(); };
    window.__MW_APPKIT_READY__ = true;
    return () => { delete window.__MW_APPKIT_OPEN__; delete window.__MW_APPKIT_DISCONNECT__; delete window.__MW_APPKIT_READY__; };
  }, [isConnected, connecting, open, disconnect]);

  // Menu: outside click + Escape.
  useEffect(() => {
    if (!menuOpen) return;
    const onDoc = (e: MouseEvent) => { if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenuOpen(false); };
    document.addEventListener('click', onDoc, true);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('click', onDoc, true); document.removeEventListener('keydown', onKey); };
  }, [menuOpen]);

  if (!isConnected && !connecting && storedAddr) {
    return <ReconnectPill addr={storedAddr} onReconnect={() => open()} onForget={() => { forgetStoredAddr(); setStoredAddr(null); }} />;
  }
  if (!isConnected) {
    return <ConnectButton onClick={() => open()} busy={connecting} label="Reconnecting…" />;
  }

  const addr = address as string;
  const copyAddress = () => { navigator.clipboard?.writeText(addr).catch(() => {}); setMenuOpen(false); window.dispatchEvent(new CustomEvent('mw-toast', { detail: { message: 'Address copied', variant: 'success' } })); };
  return (
    <div className="wc-pill wc-connected" ref={menuRef}>
      <button type="button" className="btn btn-secondary wc-trigger" aria-haspopup="menu" aria-expanded={menuOpen} onClick={() => setMenuOpen((o) => !o)}
              style={wrongNetwork ? { borderColor: 'var(--amber-35)' } : undefined}>
        <span className={`wc-dot${wrongNetwork ? ' is-warn' : ''}`} aria-hidden={wrongNetwork ? undefined : 'true'}
              title={wrongNetwork ? `Wallet is on the wrong network — switch to ${targetChain.name}` : undefined}
              aria-label={wrongNetwork ? `Wallet is on the wrong network — switch to ${targetChain.name}` : undefined} />
        <span className="mono wc-addr">{short(addr)}</span>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
      </button>
      {menuOpen && (
        <div className="wc-menu" role="menu" aria-label="Wallet">
          <button type="button" role="menuitem" className="wc-item" onClick={copyAddress}>Copy address</button>
          <a role="menuitem" className="wc-item" href={`/profile/${addr}`} onClick={() => setMenuOpen(false)}>View profile</a>
          <a role="menuitem" className="wc-item" href={explorerAddr(addr)} target="_blank" rel="noopener" onClick={() => setMenuOpen(false)}>Explorer ↗</a>
          <button type="button" role="menuitem" className="wc-item wc-item-danger wc-disconnect" onClick={() => { setMenuOpen(false); disconnect(); }}>Disconnect</button>
        </div>
      )}
    </div>
  );
}

// ── AppKit init (after the libs load) ────────────────────────────────────────
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- holds wagmi Config from adapter; type varies by adapter/version
let _wagmiConfig: any = null;
let _appKitReady = false;
let _initFailed = false;
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- QueryClient type comes from the lazily loaded module
let _queryClient: any = null;

async function initAppKit(): Promise<void> {
  if (typeof window === 'undefined') return;
  if (_appKitReady) return;
  const projectId = getProjectId();
  if (!projectId) { console.warn('[mw-wc] No Reown project ID'); _initFailed = true; return; }
  try {
    const m = await loadMods();
    const transports = { [targetChain.id]: m.wagmi.http(targetChain.rpcUrls.default.http[0]) };
    const adapter = new m.adapter.WagmiAdapter({ networks: chains, projectId, transports });
    _wagmiConfig = adapter.wagmiConfig;
    _queryClient = _queryClient || new m.rq.QueryClient();
    m.appkit.createAppKit({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any -- chains matches AppKitNetwork structurally but type defs differ
      adapters: [adapter], networks: chains as any, defaultNetwork: targetAppKitNetwork, projectId,
      // Reown validates metadata.url against the requesting origin, so it must
      // be derived: each network is its own host.
      metadata: {
        name: 'MagicWebb',
        description: 'Non-custodial NFT marketplace on ' + targetChain.name,
        url: window.location.origin,
        icons: [window.location.origin + '/favicon.ico'],
      },
      // Self-custody wallets only: no email/social login, no Coinbase SDK, no
      // analytics beacon, no onramp/swaps widgets.
      features: { analytics: false, email: false, socials: false, onramp: false, swaps: false },
      enableCoinbase: false,
      themeMode: 'dark',
    });
    _appKitReady = true;
    // Publish for the non-React world (Astro page scripts, Svelte islands):
    // lib/tx/client.ts reads window.__MW_WAGMI_CONFIG__ and waits on the
    // mw-wagmi-ready event. Published only AFTER createAppKit() succeeds.
    window.__MW_WAGMI_CONFIG__ = adapter.wagmiConfig;
    window.dispatchEvent(new CustomEvent('mw-wagmi-ready'));
  } catch (e) {
    console.error('[mw-wc] AppKit init failed:', e);
    _initFailed = true;
    _wagmiConfig = null;
    delete window.__MW_WAGMI_CONFIG__;
  }
}

function pageNeedsWallet(): boolean {
  return typeof document !== 'undefined' && !!document.querySelector('main[data-mw-needs-wallet]');
}

type Phase = 'idle' | 'loading' | 'ready' | 'failed';

export default function WalletConnect() {
  const [phase, setPhase] = useState<Phase>(() => (pageNeedsWallet() ? 'loading' : 'idle'));
  const [storedAddr, setStoredAddr] = useState<string | null>(() => (typeof window !== 'undefined' ? readStoredAddr() : null));
  const wantOpen = useRef(false);
  const started = useRef(false);

  const start = useCallback((openAfter: boolean) => {
    if (openAfter) wantOpen.current = true;
    if (started.current) return;
    started.current = true;
    setPhase('loading');
    initAppKit().then(() => setPhase(_initFailed || !_wagmiConfig ? 'failed' : 'ready'));
  }, []);

  useEffect(() => {
    if (phase === 'loading' && !started.current) start(false);
  }, [phase, start]);

  // Shim so page chrome can request the wallet before the stack exists; the
  // real WalletButton replaces it once mounted.
  useEffect(() => {
    if (phase === 'ready') return;
    window.__MW_APPKIT_OPEN__ = () => start(true);
    const onLoad = () => start(false);
    window.addEventListener('mw-wallet-load', onLoad);
    return () => {
      window.removeEventListener('mw-wallet-load', onLoad);
      if (window.__MW_APPKIT_OPEN__ && !window.__MW_APPKIT_READY__) delete window.__MW_APPKIT_OPEN__;
    };
  }, [phase, start]);

  if (phase === 'failed') {
    return (
      <div className="wc-failed" role="status">
        <span>Wallet unavailable</span>
        <button type="button" className="btn btn-secondary btn-sm"
                onClick={() => { _initFailed = false; _appKitReady = false; started.current = false; start(false); }}>Retry</button>
      </div>
    );
  }
  if (phase === 'ready' && _wagmiConfig && _mods) {
    const { WagmiProvider } = _mods.wagmi;
    const { QueryClientProvider } = _mods.rq;
    return (
      <WagmiProvider config={_wagmiConfig}>
        <QueryClientProvider client={_queryClient}>
          <WalletButton wantOpen={wantOpen} />
        </QueryClientProvider>
      </WagmiProvider>
    );
  }
  const busy = phase === 'loading';
  if (storedAddr) {
    return <ReconnectPill addr={storedAddr} busy={busy} onReconnect={() => start(false)} onForget={() => { forgetStoredAddr(); setStoredAddr(null); }} />;
  }
  return <ConnectButton busy={busy} onClick={() => start(true)} />;
}
