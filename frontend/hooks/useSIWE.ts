"use client";
import {useEffect, useState} from "react";
import {useAccount, useSignMessage} from "wagmi";
import {getToken, setToken, clearToken, isExpired} from "@/lib/auth";

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

function buildSIWEMessage(address: string, nonce: string, domain: string, chainId: number): string {
  const now = new Date();
  const exp = new Date(now.getTime() + 10 * 60 * 1000);
  return [
    `${domain} wants you to sign in with your Ethereum account:`,
    address,
    "",
    "Sign in to MagicWebb marketplace.",
    "",
    `URI: https://${domain}`,
    "Version: 1",
    `Chain ID: ${chainId}`,
    `Nonce: ${nonce}`,
    `Issued At: ${now.toISOString()}`,
    `Expiration Time: ${exp.toISOString()}`,
  ].join("\n");
}

export function useSIWE() {
  const {address, chainId} = useAccount();
  const {signMessageAsync} = useSignMessage();
  const [session, setSession] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Restore session from localStorage on mount
  useEffect(() => {
    const stored = getToken();
    if (stored && !isExpired(stored)) {
      setSession(stored);
    } else if (stored) {
      clearToken();
    }
  }, []);

  const signIn = async () => {
    if (!address || !chainId) return;
    setIsLoading(true);
    setError(null);
    try {
      const nonceRes = await fetch(`${API_BASE}/auth/nonce?address=${address}`);
      if (!nonceRes.ok) throw new Error("Failed to get nonce");
      const {nonce} = await nonceRes.json() as {nonce: string};

      const domain = window.location.host;
      const message = buildSIWEMessage(address, nonce, domain, chainId);
      const signature = await signMessageAsync({message});

      const verifyRes = await fetch(`${API_BASE}/auth/verify`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({address, message, signature}),
      });
      if (!verifyRes.ok) {
        const body = await verifyRes.json() as {error?: string};
        throw new Error(body.error ?? "Verification failed");
      }
      const {token} = await verifyRes.json() as {token: string};
      setToken(token);
      setSession(token);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setIsLoading(false);
    }
  };

  const signOut = () => {
    clearToken();
    setSession(null);
  };

  return {session, signIn, signOut, isLoading, error, isSignedIn: !!session};
}
