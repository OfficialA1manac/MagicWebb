"use client";
import type {Address, Hex} from "viem";
import {useMemo} from "react";
import {useBuy} from "@/hooks/useBuy";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";

export function BuyButton({
  coll, id, price, expiresAt = 0n
}: {coll: Address; id: bigint; price: bigint; expiresAt?: bigint}) {
  const {buy, isPending, error} = useBuy();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();

  const expired = useMemo(() => {
    if (!expiresAt || expiresAt === 0n) return false;
    return BigInt(Math.floor(Date.now() / 1000)) > expiresAt;
  }, [expiresAt]);

  return (
    <div className="space-y-2">
      <button
        className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
        disabled={expired || isPending || isConfirming}
        onClick={async () => {
          const h = await buy(coll, id, price);
          setHash(h as Hex);
        }}
      >
        {expired ? "Listing expired" : isPending ? "Confirm in wallet…" : isConfirming ? "Buying…" : "Buy now"}
      </button>
      {expired && (
        <p className="text-xs text-yellow-400">
          This listing&apos;s on-chain expiry has passed — buying would revert. Ask the seller to cancel (if needed) and list again.
        </p>
      )}
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={error} label="Buy" />
    </div>
  );
}
