"use client";
import {useAccount, useConnect, useDisconnect} from "wagmi";
import {useEffect, useMemo, useRef, useState} from "react";

const short = (a: string) => `${a.slice(0, 6)}…${a.slice(-4)}`;

/** Dedupe by connector id, prefer injected then WalletConnect for menu order. */
function orderedConnectors<T extends {id: string; name: string}>(connectors: readonly T[]): T[] {
  const byId = new Map<string, T>();
  for (const c of connectors) {
    if (!byId.has(c.id)) byId.set(c.id, c);
  }
  return Array.from(byId.values()).sort((a, b) => {
    const rank = (x: T) => (x.id === "injected" ? 0 : x.id === "walletConnect" ? 1 : 2);
    const d = rank(a) - rank(b);
    return d !== 0 ? d : a.name.localeCompare(b.name);
  });
}

export function ConnectButton() {
  const [mounted, setMounted] = useState(false);
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  useEffect(() => setMounted(true), []);
  const {address, isConnected} = useAccount();
  const {connectors, connectAsync, isPending} = useConnect();
  const {disconnect} = useDisconnect();
  const [pendingUid, setPendingUid] = useState<string | null>(null);

  const choices = useMemo(() => orderedConnectors(connectors), [connectors]);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);

  if (!mounted) {
    return (
      <button
        type="button"
        className="px-4 py-2 rounded-md bg-emerald-600 text-sm font-medium text-neutral-950 shadow-sm"
        disabled
      >
        Connect
      </button>
    );
  }

  if (isConnected && address) {
    return (
      <button
        type="button"
        onClick={() => disconnect()}
        className="px-4 py-2 rounded-md border border-neutral-600 bg-neutral-900/80 text-sm font-mono text-neutral-100 hover:border-emerald-500/60 hover:bg-neutral-800"
        title="Disconnect"
      >
        {short(address)}
      </button>
    );
  }

  const label = (c: (typeof choices)[0]) => {
    if (c.id === "walletConnect") return "WalletConnect (QR)";
    if (c.type === "injected") return "Browser extension";
    return c.name;
  };

  return (
    <div className="relative" ref={wrapRef}>
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        className="px-4 py-2 rounded-md bg-emerald-600 text-sm font-medium text-neutral-950 shadow-sm hover:bg-emerald-500"
      >
        Connect
      </button>
      {open && (
        <ul
          className="absolute right-0 z-[100] mt-2 min-w-[12rem] overflow-hidden rounded-md border border-neutral-700 bg-neutral-900 py-1 shadow-xl"
          role="menu"
        >
          {choices.map(c => (
            <li key={c.uid} role="none">
              <button
                type="button"
                role="menuitem"
                className="w-full px-4 py-2.5 text-left text-sm text-neutral-200 hover:bg-neutral-800 disabled:opacity-50"
                disabled={!!pendingUid || isPending}
                onClick={async () => {
                  setPendingUid(c.uid);
                  try {
                    await connectAsync({connector: c});
                    setOpen(false);
                  } catch {
                    /* reject */
                  } finally {
                    setPendingUid(null);
                  }
                }}
              >
                {pendingUid === c.uid ? "Connecting…" : label(c)}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
