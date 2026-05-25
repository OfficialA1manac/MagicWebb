"use client";
import {useEffect, useState} from "react";
import {formatEther, parseEther, type Address, type Hex} from "viem";
import {useBlockNumber} from "wagmi";
import {useCommitBid} from "@/hooks/useCommitBid";
import {useBid} from "@/hooks/useBid";
import {
  getPendingCommit,
  storePendingCommit,
  clearPendingCommit,
  type PendingCommit,
} from "@/hooks/useCommitBid";
import {useTx} from "@/hooks/useTx";
import {TxBanner} from "./TxBanner";
import {CURRENCY_SYMBOL} from "@/lib/addresses";
import {api} from "@/lib/api";

const COMMIT_DELAY = 2n;

type BidHistoryItem = {
  bidder: string;
  amount_wei: string;
  tx_hash: string;
  placed_at: string;
};

export function BidForm({
  id,
  minNext,
  highestBid,
  highestBidder,
}: {
  id: bigint;
  minNext: bigint;
  highestBid: bigint;
  highestBidder: Address;
}) {
  const {data: blockNumber} = useBlockNumber({watch: true});
  const [amt, setAmt] = useState("");
  const [phase, setPhase] = useState<"idle" | "committing" | "committed" | "revealing" | "done">("idle");
  const [pending, setPending] = useState<PendingCommit | null>(null);
  const [bidHistory, setBidHistory] = useState<BidHistoryItem[]>([]);

  const {commitBid, isPending: commitPending, error: commitErr} = useCommitBid();
  const {bid, isPending: revealPending, error: revealErr} = useBid();
  const {hash, setHash, isConfirming, isConfirmed, txError, reset} = useTx();

  useEffect(() => {
    const c = getPendingCommit(id);
    if (c) {
      setPending(c);
      setPhase("committed");
    }
  }, [id]);

  useEffect(() => {
    api.getAuctionBids(id).then(setBidHistory).catch(() => {});
  }, [id, isConfirmed]);

  useEffect(() => {
    if (isConfirmed && phase === "committing" && blockNumber && pending) {
      const updated = {...pending, commitBlock: Number(blockNumber)};
      storePendingCommit(updated);
      setPending(updated);
      setPhase("committed");
      reset();
    }
  }, [isConfirmed, phase, blockNumber, pending, reset]);

  useEffect(() => {
    if (isConfirmed && phase === "revealing") {
      clearPendingCommit(id);
      setPending(null);
      setPhase("done");
    }
  }, [isConfirmed, phase, id]);

  const parsed = (() => {
    try { return amt ? parseEther(amt) : null; } catch { return null; }
  })();
  const tooLow = parsed !== null && parsed < minNext;
  const canCommit = !!parsed && !tooLow;

  const canReveal =
    pending !== null &&
    blockNumber !== undefined &&
    BigInt(pending.commitBlock) + COMMIT_DELAY <= blockNumber;

  const blocksLeft =
    pending && blockNumber
      ? Number(BigInt(pending.commitBlock) + COMMIT_DELAY - blockNumber)
      : null;

  const handleCommit = async () => {
    if (!parsed) return;
    try {
      const {hash: h} = await commitBid(id, parsed);
      setHash(h as Hex);
      setPhase("committing");
    } catch {}
  };

  const handleReveal = async () => {
    if (!pending) return;
    try {
      const fullAmount = BigInt(pending.fullAmount);
      const isLeader = pending.bidder.toLowerCase() === highestBidder?.toLowerCase();
      const value = isLeader ? fullAmount - highestBid : fullAmount;
      setPhase("revealing");
      const h = await bid(id, fullAmount, pending.salt, value);
      setHash(h as Hex);
    } catch {
      setPhase("committed");
    }
  };

  if (phase === "done") {
    return (
      <div className="space-y-4">
        <p className="text-sm text-emerald-400">Bid placed successfully.</p>
        <BidHistory items={bidHistory} />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {phase === "idle" && (
        <div className="space-y-2">
          <p className="text-xs text-neutral-500">
            Step 1 of 2 — commit a bid. After ~2 blocks (~4 s on Flare) you reveal it.
            This prevents front-running.
          </p>
          <label className="block text-sm">
            Bid amount ({CURRENCY_SYMBOL}) — min {formatEther(minNext)}
            <input
              className="mt-1 w-full bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
              placeholder={formatEther(minNext)}
              value={amt}
              onChange={e => setAmt(e.target.value)}
            />
          </label>
          {tooLow && (
            <p className="text-xs text-red-400">
              Min bid: {formatEther(minNext)} {CURRENCY_SYMBOL}
            </p>
          )}
          <button
            className="w-full sm:w-auto px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50"
            disabled={commitPending || isConfirming || !canCommit}
            onClick={handleCommit}
          >
            {commitPending ? "Confirm in wallet…" : isConfirming ? "Committing…" : "Commit bid"}
          </button>
          <TxBanner
            hash={hash}
            isConfirming={isConfirming}
            isConfirmed={isConfirmed}
            error={commitErr ?? txError}
            label="Commit"
          />
        </div>
      )}

      {(phase === "committing" || phase === "committed" || phase === "revealing") && (
        <div className="space-y-2">
          <p className="text-xs text-neutral-500">Step 2 of 2 — reveal your bid.</p>
          {!canReveal && blocksLeft !== null && blocksLeft > 0 && (
            <p className="text-sm text-amber-400/90">
              Waiting for {blocksLeft} more block{blocksLeft !== 1 ? "s" : ""}…
            </p>
          )}
          {phase === "committing" && (
            <p className="text-sm text-neutral-400">Confirming commit transaction…</p>
          )}
          <button
            className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
            disabled={!canReveal || revealPending || isConfirming}
            onClick={handleReveal}
          >
            {revealPending
              ? "Confirm in wallet…"
              : isConfirming
              ? "Revealing…"
              : canReveal
              ? `Reveal bid (${formatEther(BigInt(pending?.fullAmount ?? "0"))} ${CURRENCY_SYMBOL})`
              : "Waiting for blocks…"}
          </button>
          <TxBanner
            hash={hash}
            isConfirming={isConfirming}
            isConfirmed={isConfirmed}
            error={revealErr ?? txError}
            label="Bid"
          />
          <button
            className="text-xs text-neutral-600 hover:text-neutral-400"
            onClick={() => {
              clearPendingCommit(id);
              setPending(null);
              setPhase("idle");
              reset();
            }}
          >
            Cancel (forfeit commit)
          </button>
        </div>
      )}

      <BidHistory items={bidHistory} />
    </div>
  );
}

function BidHistory({items}: {items: BidHistoryItem[]}) {
  if (items.length === 0) return null;
  return (
    <div className="space-y-1">
      <div className="text-xs font-medium text-neutral-500 uppercase tracking-wide">Bid history</div>
      <div className="divide-y divide-neutral-800 rounded border border-neutral-800">
        {items.map((b, i) => (
          <div key={i} className="flex items-center justify-between px-3 py-2 text-xs">
            <span className="font-mono text-neutral-400 truncate max-w-[120px]">
              {b.bidder.slice(0, 6)}…{b.bidder.slice(-4)}
            </span>
            <span className="font-semibold">
              {formatEther(BigInt(b.amount_wei))} {CURRENCY_SYMBOL}
            </span>
            <span className="text-neutral-600 hidden sm:block">
              {new Date(b.placed_at).toLocaleTimeString()}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
