// Bridge between the React WalletConnect island (which owns the AppKit /
// wagmi config) and the rest of the UI (Astro pages + Svelte islands).
import { getAccount, getPublicClient, getWalletClient, switchChain, watchAccount, type Config } from '@wagmi/core';
import type { Address, PublicClient, WalletClient } from 'viem';
import { currentChain } from '../chains';
import { TxError } from './errors';

export function getWagmiConfig(): Config | null {
  return (typeof window !== 'undefined' && window.__MW_WAGMI_CONFIG__) || null;
}

/** Resolves once the wallet island has published its wagmi config. */
export function waitForWagmi(timeoutMs = 15000): Promise<Config> {
  const have = getWagmiConfig();
  if (have) return Promise.resolve(have);
  return new Promise((res, rej) => {
    const on = () => { clearTimeout(t); window.removeEventListener('mw-wagmi-ready', on); res(getWagmiConfig() as Config); };
    const t = setTimeout(() => {
      window.removeEventListener('mw-wagmi-ready', on);
      rej(new TxError('WalletRequired', 'Wallet is still loading. Try again in a moment.'));
    }, timeoutMs);
    window.addEventListener('mw-wagmi-ready', on);
  });
}

export interface WalletCtx {
  config: Config;
  account: Address;
  wallet: WalletClient;
  pub: PublicClient;
}

/**
 * Connected, on the right chain, or throws a TxError the TxModal knows how to
 * render. Opens the AppKit modal when disconnected so the next click is the
 * wallet, not a dead button.
 */
export async function requireWallet(): Promise<WalletCtx> {
  const config = await waitForWagmi();
  const chain = currentChain();
  let acct = getAccount(config);
  if (!acct.isConnected || !acct.address) {
    window.__MW_APPKIT_OPEN__?.();
    acct = await new Promise((res) => {
      const un = watchAccount(config, { onChange(a) { if (a.isConnected && a.address) { un(); res(a); } } });
      setTimeout(() => { un(); res(getAccount(config)); }, 120_000);
    });
    if (!acct.isConnected || !acct.address) throw new TxError('WalletRequired', 'Connect a wallet to continue.');
  }
  if (acct.chainId !== chain.id) {
    try {
      await switchChain(config, { chainId: chain.id });
    } catch (e) {
      throw new TxError('WrongChain', `Your wallet is on a different network. Switch it to ${chain.name} to continue.`, { cause: e });
    }
  }
  const wallet = await getWalletClient(config, { chainId: chain.id });
  const pub = getPublicClient(config, { chainId: chain.id });
  if (!pub) throw new TxError('RpcError', 'No RPC client available.');
  return { config, account: acct.address as Address, wallet: wallet as unknown as WalletClient, pub: pub as unknown as PublicClient };
}

/** Read-only client, no wallet needed (public pages). */
export async function publicClient(): Promise<PublicClient> {
  const config = await waitForWagmi();
  const pub = getPublicClient(config, { chainId: currentChain().id });
  if (!pub) throw new TxError('RpcError', 'No RPC client available.');
  return pub as unknown as PublicClient;
}

export function connectedAddress(): Address | null {
  const c = getWagmiConfig(); if (!c) return null;
  const a = getAccount(c); return a.isConnected && a.address ? (a.address as Address) : null;
}

export function connectedChainId(): number | null {
  const c = getWagmiConfig(); if (!c) return null;
  return getAccount(c).chainId ?? null;
}

/** Subscribe to wallet account/chain changes. Returns unsubscribe. */
export function onAccountChange(fn: (a: { address: Address | null; chainId: number | null }) => void): () => void {
  let un: (() => void) | null = null;
  let cancelled = false;
  waitForWagmi(60_000).then((config) => {
    if (cancelled) return;
    fn({ address: connectedAddress(), chainId: connectedChainId() });
    un = watchAccount(config, { onChange(a) { fn({ address: a.isConnected && a.address ? (a.address as Address) : null, chainId: a.chainId ?? null }); } });
  }).catch(() => {});
  return () => { cancelled = true; un?.(); };
}
