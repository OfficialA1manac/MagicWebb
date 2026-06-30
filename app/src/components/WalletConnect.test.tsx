import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// ── Shared mock instance references ─────────────────────────────────────────
// Stored at module level so they persist across tests and can be asserted on
// without needing to re-import the mock module.
const mockFns = {
  open: vi.fn(),
  disconnect: vi.fn(),
  switchNetwork: vi.fn(),
};

// ── Mutable state objects for dynamic mock return values ────────────────────
// These are mutated directly in tests to simulate different wallet states.
// The mock factory reads from these on every call, so no dynamic re-mocking
// or module resetting is needed.
let accountState = {
  address: undefined as string | undefined,
  isConnected: false,
  status: 'disconnected',
};

let networkState = {
  chainId: 114,
};

// ── Mock all external dependencies ──────────────────────────────────────────
// vi.mock is hoisted to the top of the file, so these run before any imports.
// The env var import.meta.env.PUBLIC_REOWN_PROJECT_ID is mocked via the
// `define` option in vitest.config.mts so initAppKit() succeeds.

vi.mock('@reown/appkit/react', () => ({
  createAppKit: vi.fn(),
  useAppKit: () => ({ open: mockFns.open }),
  useDisconnect: () => ({ disconnect: mockFns.disconnect }),
  useAppKitAccount: vi.fn(() => ({ ...accountState })),
  useAppKitNetwork: vi.fn(() => ({
    ...networkState,
    switchNetwork: mockFns.switchNetwork,
  })),
}));

vi.mock('@reown/appkit-adapter-wagmi', () => {
  // Use a class so `new WagmiAdapter(...)` works (arrow functions can't be
  // used as constructors). The mock implementation returns an object with
  // the wagmiConfig property the component expects.
  class MockWagmiAdapter {
    wagmiConfig: any;
    constructor(_opts: any) {
      this.wagmiConfig = { mock: true };
    }
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

// ── Suite ───────────────────────────────────────────────────────────────────

describe('WalletConnect', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset mutable state to defaults
    accountState = { address: undefined, isConnected: false, status: 'disconnected' };
    networkState = { chainId: 114 };
    // Clear browser state so prior test cases don't leak via localStorage
    try { localStorage.clear(); } catch (_) {}
  });

  it('renders loading state when init is pending', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    // The useEffect fires initAppKit().then(() => setReady(true)) asynchronously,
    // so "Loading…" is visible on the synchronous first render.
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });

  it('renders connect button when not connected', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const btn = await screen.findByText('Connect Wallet', {}, { timeout: 3000 });
    expect(btn).toBeInTheDocument();
  });

  it('opens AppKit modal on connect click', async () => {
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const btn = await screen.findByText('Connect Wallet', {}, { timeout: 3000 });
    fireEvent.click(btn);
    expect(mockFns.open).toHaveBeenCalledTimes(1);
  });

  it('shows connected state with truncated address', async () => {
    accountState = {
      address: '0x1234567890abcdef1234567890abcdef12345678',
      isConnected: true,
      status: 'connected',
    };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText(/0x1234.*5678/, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByText('Disconnect')).toBeInTheDocument();
  });

  it('shows wrong network warning and switches on click', async () => {
    accountState = {
      address: '0x1234567890abcdef1234567890abcdef12345678',
      isConnected: true,
      status: 'connected',
    };
    networkState = { chainId: 999 }; // wrong network — target chain is 114
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText(/Wrong Network/, {}, { timeout: 3000 })).toBeInTheDocument();
    const switchBtn = screen.getByText(/Switch to Flare/);
    fireEvent.click(switchBtn);
    expect(mockFns.switchNetwork).toHaveBeenCalled();
  });

  it('shows reconnect button when stored wallet is in localStorage', async () => {
    localStorage.setItem('mw_addr', '0x1234567890abcdef1234567890abcdef12345678');
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText('Reconnect', {}, { timeout: 3000 })).toBeInTheDocument();
  });

  it('clears stored wallet when forget button is clicked', async () => {
    localStorage.setItem('mw_addr', '0x1234567890abcdef1234567890abcdef12345678');
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const forgetBtn = await screen.findByTitle('Forget wallet', {}, { timeout: 3000 });
    fireEvent.click(forgetBtn);
    expect(localStorage.getItem('mw_addr')).toBeNull();
  });

  it('copies address to clipboard when address is clicked', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });

    accountState = {
      address: '0x1234567890abcdef1234567890abcdef12345678',
      isConnected: true,
      status: 'connected',
    };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    const addrEl = await screen.findByText(/0x1234.*5678/, {}, { timeout: 3000 });
    fireEvent.click(addrEl);
    expect(writeText).toHaveBeenCalledWith('0x1234567890abcdef1234567890abcdef12345678');
  });

  it('shows loading state during reconnecting status', async () => {
    accountState = {
      address: undefined,
      isConnected: false,
      status: 'reconnecting',
    };
    const WalletConnect = (await import('./WalletConnect')).default;
    render(<WalletConnect />);
    expect(await screen.findByText('Reconnecting…', {}, { timeout: 3000 })).toBeInTheDocument();
  });
});
