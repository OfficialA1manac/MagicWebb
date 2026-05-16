"use client";
import {useState} from "react";
import {parseEther, formatEther, type Hex} from "viem";
import {useBid} from "@/hooks/useBid";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";

export function BidForm({id, minNext}: {id: bigint; minNext: bigint}) {
  const [amt, setAmt] = useState("");
  const {bid, isPending, error: writeError} = useBid();
  const {hash, setHash, isConfirming, isConfirmed, txError} = useTx();

  const parsed = (() => {
    try { return amt ? parseEther(amt) : null; } catch { return null; }
  })();
  const tooLow = parsed !== null && parsed < minNext;
  const canBid = !!parsed && !tooLow;

  return (
    <div className="space-y-2">
      <label className="block text-sm">
        Bid amount (C2FLR) — min {formatEther(minNext)}
        <input
          className="mt-1 w-full bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
          placeholder={formatEther(minNext)}
          value={amt}
          onChange={e => setAmt(e.target.value)}
        />
      </label>
      {tooLow && (
        <p className="text-xs text-red-400">Bid must be at least {formatEther(minNext)} C2FLR.</p>
      )}
      <button
        className="w-full sm:w-auto px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50"
        disabled={isPending || isConfirming || !canBid}
        onClick={async () => {
          if (!parsed) return;
          try {
            const h = await bid(id, parsed);
            setHash(h as Hex);
          } catch { /* wagmi error state handles display */ }
        }}
      >{isPending ? "Confirm in wallet…" : isConfirming ? "Bidding…" : "Place bid"}</button>
      <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={writeError ?? txError} label="Bid" />
    </div>
  );
}
