"use client";
import type {Address, Hex} from "viem";
import {useBuy} from "@/hooks/useBuy";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";

export function BuyButton({coll, id, price}: {coll: Address; id: bigint; price: bigint}) {
  const {buy, isPending, error} = useBuy();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();
  return (
    <div className="space-y-2">
      <button
        className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
        disabled={isPending || isConfirming}
        onClick={async () => {
          const h = await buy(coll, id, price);
          setHash(h as Hex);
        }}
      >{isPending ? "Confirm in wallet…" : isConfirming ? "Buying…" : "Buy now"}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={error} label="Buy" />
    </div>
  );
}
