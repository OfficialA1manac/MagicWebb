import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';

// ── Shared mock instance references ─────────────────────────────────────────
const mockFns = {
  open: vi.fn(),
  disconnect: vi.fn(),
  switchNetwork: vi.fn(),
  createAppKit: vi.fn(),
};

// Mutable state read by the mock hooks on every call.
let accountState = {
  address: undefined as string | undefined,
  isConnected: false,
  status: 'disconnected',
};
let networkState = { chainId: 114 };

// ── Mock the wallet stack (loaded lazily via dynamic import) ─────────────────
// import.meta.env.PUBLIC_REOWN_PROJECT_ID is defined in vitest.config.mts so
// initAppKit() succeeds.
vi.mock('@reown/appkit/react', () => ({
  createAppKit: (...a: unknown[]) => mockFns.createAppKit(...a),
  useAppKit: () => ({ open: mockFns.open }),
  useDisconnect: () => ({ disconnect: mockFns.disconnect }),
  useAppKitAccount: vi.fn(() => ({ ...accountState })),
  useAppKitNetwork: vi.fn(() => ({ ...networkState, switchNetwork: mockFns.switchNetwork })),
}));

vi.mock('@reown/appkit-adapter-wagmi', () => {
  class MockWagmiAdapter {
    wagmiConfig: unknown;
    constructor(_opts: unknown) { this.wagmiConfig = { mock: true }; }
  }
  return { WagmiAdapter: MockWagmiAdapter };
});

vi.mock('wagmi', () => ({
  WagmiProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useDisconnect: () => ({ disconnect: mockFns.disconnect }),
  http: () => vi.fn(),
}));

vi.mock('@tanstack/react-query', () => ({
  QueryClient: vi.fn(),
  QueryClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

/** Mark the page as one whose islands need the wallet at mount. */
function needsWallet() {
  const main = document.createElement('main');
  main.setAttribute('data-mw-needs-wallet', '');
  document.body.appendChild(main);
}

const ADDR = '0x1234567890abcdef1234567890abcdef12345678';

// ── Suite ───────────────────────────────────────────────────────────────────

describe('WalletConnect', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    accountState = { address: undefined, isConnected: false, status: 'disconnected' };
    networkState = { chainId: 114 };
    try { localStorage.clear(); } catch (_) {}
  });
  afterEach(() => {
    document.body.innerHTML = '';
    delete window.__MW_APPKIT_OPEN__;
    delete window.__MW_APPKIT_READY__;
  });

  it('renders a light Connect button without loading AppKit on a plain page', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(screen.getByText('Connect wallet')).toBeInTheDocument();
    expect(screen.getByText('Connect')).toBeInTheDocument(); // ≤480px label (CSS-toggled)
    await act(async () => {});
    expect(mockFns.createAppKit).not.toHaveBeenCalled();
  });

  it('renders loading state at mount when the page needs the wallet', async () => {
    needsWallet();
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    await act(async () => {});
  });

  it('loads the stack and opens AppKit on first Connect click', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    fireEvent.click(screen.getByText('Connect wallet'));
    await waitFor(() => expect(mockFns.open).toHaveBeenCalledTimes(1), { timeout: 3000 });
  });

  it('exposes a __MW_APPKIT_OPEN__ shim that loads then opens', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(typeof window.__MW_APPKIT_OPEN__).toBe('function');
    act(() => { window.__MW_APPKIT_OPEN__!(); });
    await waitFor(() => expect(mockFns.open).toHaveBeenCalledTimes(1), { timeout: 3000 });
  });

  it('shows connected pill with truncated address and a menu with Disconnect', async () => {
    needsWallet();
    accountState = { address: ADDR, isConnected: true, status: 'connected' };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const trigger = await screen.findByText(/0x1234.*5678/, {}, { timeout: 3000 });
    expect(trigger).toBeInTheDocument();
    fireEvent.click(trigger);
    expect(screen.getByText('Disconnect')).toBeInTheDocument();
    expect(screen.getByText('Copy address')).toBeInTheDocument();
    expect(screen.getByText('View profile')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Disconnect'));
    expect(mockFns.disconnect).toHaveBeenCalledTimes(1);
  });

  it('shows a compact wrong-network warning dot (banner owns the message)', async () => {
    needsWallet();
    accountState = { address: ADDR, isConnected: true, status: 'connected' };
    networkState = { chainId: 999 };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText(/0x1234.*5678/, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByTitle(/wrong network/i)).toBeInTheDocument();
    expect(screen.queryByText(/Wrong Network/)).toBeNull();
    expect(screen.queryByText(/Switch to Flare/)).toBeNull();
  });

  it('shows the reconnect pill from localStorage without loading AppKit', async () => {
    localStorage.setItem('mw_addr', ADDR);
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText('Reconnect', {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByText('0x1234…5678')).toBeInTheDocument();
    await act(async () => {});
    expect(mockFns.createAppKit).not.toHaveBeenCalled();
  });

  it('clears stored wallet when forget button is clicked', async () => {
    localStorage.setItem('mw_addr', ADDR);
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const forgetBtn = await screen.findByTitle('Forget wallet', {}, { timeout: 3000 });
    fireEvent.click(forgetBtn);
    expect(localStorage.getItem('mw_addr')).toBeNull();
    expect(screen.getByText('Connect wallet')).toBeInTheDocument();
  });

  it('copies address to clipboard from the menu', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    needsWallet();
    accountState = { address: ADDR, isConnected: true, status: 'connected' };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    fireEvent.click(await screen.findByText(/0x1234.*5678/, {}, { timeout: 3000 }));
    fireEvent.click(screen.getByText('Copy address'));
    expect(writeText).toHaveBeenCalledWith(ADDR);
  });

  it('shows reconnecting state while wagmi rehydrates', async () => {
    needsWallet();
    accountState = { address: undefined, isConnected: false, status: 'reconnecting' };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText('Reconnecting…', {}, { timeout: 3000 })).toBeInTheDocument();
  });
});
