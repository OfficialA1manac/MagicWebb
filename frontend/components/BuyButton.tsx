"use client";
import type {Address} from "viem";
import {useBuy} from "@/hooks/useBuy";

export function BuyButton({coll, id, price}: {coll: Address; id: bigint; price: bigint}) {
  const {buy, isPending, error} = useBuy();
  return (
    <div className="space-y-2">
      <button
        className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
        disabled={isPending}
        onClick={() => buy(coll, id, price)}
      >{isPending ? "Buying..." : "Buy now"}</button>
      {error && <div className="text-sm text-red-400">{error.message}</div>}
    </div>
  );
}
