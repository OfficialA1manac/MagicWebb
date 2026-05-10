"use client";
import {useState} from "react";
import {parseEther} from "viem";
import {useBid} from "@/hooks/useBid";

export function BidForm({id, minNext}: {id: bigint; minNext: bigint}) {
  const [amt, setAmt] = useState("");
  const {bid, isPending, error} = useBid();
  return (
    <div className="space-y-2">
      <input
        className="w-full bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
        placeholder={`min ${minNext} wei`}
        value={amt}
        onChange={e => setAmt(e.target.value)}
      />
      <button
        className="px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50"
        disabled={isPending || !amt}
        onClick={() => bid(id, parseEther(amt))}
      >{isPending ? "Bidding..." : "Place bid"}</button>
      {error && <div className="text-sm text-red-400">{error.message}</div>}
    </div>
  );
}
