"use client";
import {useAccount, useChainId, useSwitchChain} from "wagmi";
import {CHAIN_ID} from "@/lib/addresses";

export function NetworkGuard() {
  const {isConnected} = useAccount();
  const cid = useChainId();
  const {switchChain, isPending} = useSwitchChain();
  if (!isConnected || cid === CHAIN_ID) return null;
  return (
    <div className="bg-yellow-900/40 border-y border-yellow-700 p-3 text-center text-sm">
      Wrong network ({cid}). MagicWebb runs on Coston2 ({CHAIN_ID}).{" "}
      <button
        className="underline disabled:opacity-50"
        disabled={isPending}
        onClick={() => switchChain({chainId: CHAIN_ID})}
      >Switch</button>
    </div>
  );
}
