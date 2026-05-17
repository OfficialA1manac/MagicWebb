"use client";
import {useEffect, useState} from "react";
import {useAccount, useChainId, useSwitchChain, useWalletClient} from "wagmi";
import {CHAIN_ID, RPC_URL, CHAIN_NAME, CURRENCY_SYMBOL, EXPLORER_URL} from "@/lib/addresses";
import {coston2} from "@/lib/chains";

export function NetworkGuard() {
  const [mounted, setMounted] = useState(false);
  useEffect(() => { setMounted(true); }, []);

  const {isConnected} = useAccount();
  const cid = useChainId();
  const {switchChainAsync, isPending: switchPending} = useSwitchChain();
  const {data: walletClient} = useWalletClient();
  const [addPending, setAddPending] = useState(false);
  const [lastErr, setLastErr] = useState<string | null>(null);

  // Defer all rendering until after hydration — prevents "update while rendering Hydrate".
  if (!mounted) return null;
  if (!isConnected || cid === CHAIN_ID) return null;

  const onSwitch = async () => {
    setLastErr(null);
    try {
      await switchChainAsync({chainId: CHAIN_ID});
    } catch (e) {
      setLastErr((e as Error).message?.split("\n")[0] ?? "Could not switch network.");
    }
  };

  const onAddChain = async () => {
    if (!walletClient) return;
    setLastErr(null);
    setAddPending(true);
    try {
      await walletClient.addChain({chain: coston2});
    } catch (e) {
      setLastErr((e as Error).message?.split("\n")[0] ?? "Could not add network.");
    } finally {
      setAddPending(false);
    }
  };

  return (
    <div className="border-y border-yellow-700 bg-yellow-950/50 px-3 py-3 text-center text-sm text-yellow-100">
      <div className="font-medium">
        Wrong network — wallet is on chain <span className="font-mono">{cid}</span>; MagicWebb needs{" "}
        <span className="font-mono">{CHAIN_NAME} ({CHAIN_ID})</span>.
      </div>
      <div className="mt-2 flex flex-wrap items-center justify-center gap-2">
        <button
          type="button"
          className="rounded-md bg-yellow-600 px-3 py-1.5 text-sm font-medium text-neutral-950 hover:bg-yellow-500 disabled:opacity-50"
          disabled={switchPending}
          onClick={onSwitch}
        >
          {switchPending ? "Switching…" : `Switch to ${CHAIN_NAME}`}
        </button>
        <button
          type="button"
          className="rounded-md border border-yellow-600/80 px-3 py-1.5 text-sm hover:bg-yellow-900/40 disabled:opacity-50"
          disabled={addPending || !walletClient}
          onClick={onAddChain}
          title={`If ${CHAIN_NAME} is missing in your wallet, add it first`}
        >
          {addPending ? "Adding…" : `Add ${CHAIN_NAME} to wallet`}
        </button>
      </div>
      {lastErr && (
        <div className="mt-2 mx-auto max-w-xl text-xs text-red-300 break-words">{lastErr}</div>
      )}
      <details className="mt-3 max-w-xl mx-auto text-left text-xs text-yellow-200/80">
        <summary className="cursor-pointer select-none text-yellow-300">Manual RPC (if buttons fail)</summary>
        <ul className="mt-2 list-inside list-disc space-y-1 font-mono text-[11px] text-yellow-100/90">
          <li>Network name: {CHAIN_NAME}</li>
          <li>Chain ID: {CHAIN_ID}</li>
          <li>Currency: {CURRENCY_SYMBOL} (18 decimals)</li>
          <li className="break-all">RPC URL: {RPC_URL}</li>
          <li className="break-all">Explorer: {EXPLORER_URL}</li>
        </ul>
      </details>
    </div>
  );
}
