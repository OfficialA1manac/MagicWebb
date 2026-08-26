import { describe, it, expect } from 'vitest';
import { buildMessage } from './siwe';
import { currentChain } from '../chains';

// Pins the EIP-4361 shape the backend verifier parses structurally
// (backend/cmd/server/main.go: siweDomainMatches, siweChainIDMatches, nonce
// containment). The legacy "Sign in to MagicWebb" format fails the domain
// check with 401 "domain mismatch" on EVERY sign-in — this test exists so
// client and server can never drift apart again.
describe('buildMessage (EIP-4361)', () => {
  const addr = '0x00000000000000000000000000000000000000aa' as `0x${string}`;
  const nonce = 'abc123nonce';

  it('first line is "<domain> wants you to sign in with your Ethereum account:"', () => {
    const first = buildMessage(addr, nonce).split('\n')[0];
    expect(first).toBe(`${window.location.host} wants you to sign in with your Ethereum account:`);
  });

  it('second line is the address', () => {
    expect(buildMessage(addr, nonce).split('\n')[1]).toBe(addr);
  });

  it('contains an exact "Chain ID: <id>" line for the running chain', () => {
    const lines = buildMessage(addr, nonce).split('\n');
    expect(lines).toContain(`Chain ID: ${currentChain().id}`);
  });

  it('contains the nonce (server checks containment after signature)', () => {
    expect(buildMessage(addr, nonce)).toContain(nonce);
  });

  it('never uses the legacy pre-4361 format', () => {
    expect(buildMessage(addr, nonce).startsWith('Sign in to MagicWebb')).toBe(false);
  });
});
