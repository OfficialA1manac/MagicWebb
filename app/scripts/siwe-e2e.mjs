// Live E2E: SIWE sign-in + authenticated profile write against production.
// Uses a THROWAWAY locally-generated key (no funds needed — signing only).
// Mirrors app/src/lib/auth/siwe.ts buildMessage exactly.
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts';

const ORIGIN = process.env.MW_ORIGIN || 'https://magicwebb.fly.dev';
const HOST = new URL(ORIGIN).host;
const CHAIN_ID = Number(process.env.MW_CHAIN || 114);

const pk = generatePrivateKey();
const account = privateKeyToAccount(pk);
const addr = account.address.toLowerCase();
console.log('throwaway address:', addr);

const nonceRes = await fetch(`${ORIGIN}/auth/nonce?address=${addr}`);
if (!nonceRes.ok) throw new Error(`nonce HTTP ${nonceRes.status}`);
const { nonce } = await nonceRes.json();
console.log('nonce ok');

const message =
  `${HOST} wants you to sign in with your Ethereum account:\n` +
  `${account.address}\n\n` +
  `URI: ${ORIGIN}\nVersion: 1\nChain ID: ${CHAIN_ID}\nNonce: ${nonce}\n` +
  `Issued At: ${new Date().toISOString()}`;

const signature = await account.signMessage({ message });
const verifyRes = await fetch(`${ORIGIN}/auth/verify`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ address: addr, message, signature }),
});
const verifyBody = await verifyRes.json().catch(() => ({}));
console.log('verify:', verifyRes.status, JSON.stringify(verifyBody).slice(0, 120));
if (!verifyRes.ok) throw new Error('SIWE VERIFY FAILED — bug 1 not fixed');
const token = verifyBody.token;

const putRes = await fetch(`${ORIGIN}/api/v1/profile/${addr}`, {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
  body: JSON.stringify({ display_name: 'e2e-check', bio: '', avatar_uri: '', twitter: '', website: '' }),
});
console.log('profile PUT:', putRes.status);
if (!putRes.ok) throw new Error('authenticated profile save failed');

const getRes = await fetch(`${ORIGIN}/api/v1/profile/${addr}`);
const prof = await getRes.json();
console.log('roundtrip display_name:', prof.display_name);
if (prof.display_name !== 'e2e-check') throw new Error('profile roundtrip mismatch');
console.log('E2E SIGN-IN + PROFILE SAVE: PASS');
