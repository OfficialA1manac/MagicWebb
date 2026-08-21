// Sign-In-With-Ethereum against /auth/nonce + /auth/verify. The server sets
// an HttpOnly session cookie; the JWT is also kept in memory for the
// Authorization header (saved searches, notifications, profile edits).
import type { Address } from 'viem';
import { currentChain } from '../chains';
import { requireWallet } from '../tx/client';
import { TxError } from '../tx/errors';

export interface Session { address: Address; token: string; expiresAt: number }

let session: Session | null = null;
let inflight: Promise<Session> | null = null;

export function getSession(): Session | null {
  if (session && session.expiresAt > Date.now()) return session;
  return null;
}

export function clearSession(): void { session = null; }

function buildMessage(address: Address, nonce: string): string {
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  return `Sign in to MagicWebb\nURI: ${origin}\nChain ID: ${currentChain().id}\nAddress: ${address}\nNonce: ${nonce}`;
}

/** Connect (if needed), sign the SIWE message, exchange it for a session. Deduplicated. */
export function authenticate(): Promise<Session> {
  const have = getSession();
  if (have) return Promise.resolve(have);
  if (inflight) return inflight;
  inflight = (async () => {
    const { account, wallet } = await requireWallet();
    const nonceRes = await fetch(`/auth/nonce?address=${account}`);
    if (!nonceRes.ok) throw new TxError('RpcError', `Could not start sign-in (HTTP ${nonceRes.status}).`);
    const { nonce } = (await nonceRes.json()) as { nonce: string };
    const message = buildMessage(account, nonce);
    let signature: string;
    try {
      signature = await wallet.signMessage({ account, message });
    } catch (e) {
      throw new TxError('UserRejected', 'You declined to sign in. Nothing was sent.', { cause: e });
    }
    const verifyRes = await fetch('/auth/verify', {
      method: 'POST', credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address: account, message, signature }),
    });
    if (!verifyRes.ok) throw new TxError('RpcError', `Sign-in was rejected (HTTP ${verifyRes.status}).`);
    const { token, expires_in } = (await verifyRes.json()) as { token?: string; expires_in?: number };
    if (!token) throw new TxError('RpcError', 'Sign-in returned no session.');
    session = { address: account, token, expiresAt: Date.now() + ((expires_in ?? 3600) - 30) * 1000 };
    if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent('mw-auth-changed', { detail: { address: account } }));
    return session;
  })().finally(() => { inflight = null; });
  return inflight;
}

/** fetch() that signs in on demand and retries once on 401. */
export async function authFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const withAuth = (s: Session): RequestInit => ({
    ...init, credentials: 'include',
    headers: { ...(init.headers as Record<string, string> | undefined), Authorization: `Bearer ${s.token}`, ...(init.body && !(init.headers as Record<string, string> | undefined)?.['Content-Type'] ? { 'Content-Type': 'application/json' } : {}) },
  });
  const s = await authenticate();
  let res = await fetch(input, withAuth(s));
  if (res.status === 401) {
    clearSession();
    const s2 = await authenticate();
    res = await fetch(input, withAuth(s2));
  }
  return res;
}

export async function logout(): Promise<void> {
  clearSession();
  try { await fetch('/auth/logout', { method: 'POST', credentials: 'include' }); } catch { /* best effort */ }
  if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent('mw-auth-changed', { detail: { address: null } }));
}
