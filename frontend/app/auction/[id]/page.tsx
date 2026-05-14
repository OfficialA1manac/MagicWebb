"use client";
import {useParams} from "next/navigation";
import {useReadContract, useAccount} from "wagmi";
import {formatEther, type Address, type Hex} from "viem";
import {useEffect, useMemo, useRef, useState} from "react";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";
import {BidForm} from "@/components/BidForm";
import {TxBanner} from "@/components/TxBanner";
import {useSettleAuction} from "@/hooks/useSettleAuction";
import {useTx} from "@/hooks/useTx";

/** Matches `AuctionHouse.auctions` getter (12 fields). */
type Auction = readonly [
  Address,
  bigint,
  number,
  boolean,
  number,
  Address,
  bigint,
  bigint,
  bigint,
  bigint,
  Address,
  bigint
];

export default function AuctionPage() {
  const {id} = useParams<{id: string}>();
  const aid = BigInt(id);
  const {data, isLoading} = useReadContract({
    address: ADDR.auction,
    abi: AuctionHouseAbi,
    functionName: "auctions",
    args: [aid]
  });
  const {settle, isPending, error} = useSettleAuction();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();
  const {isConnected} = useAccount();
  const autoSettleAttempted = useRef(false);
  const [settlePhase, setSettlePhase] = useState<"idle" | "awaiting_wallet" | "done" | "error">("idle");

  const derived = useMemo(() => {
    if (!data) return null;
    const a = data as Auction;
    const [seller, , minIncBps, settled, , collection, endsAt, tokenId, reserve, highestBid, highestBidder] = a;
    const live = !settled && BigInt(Math.floor(Date.now() / 1000)) < endsAt;
    const hasBid = highestBidder !== "0x0000000000000000000000000000000000000000";
    const minNext = !hasBid
      ? reserve === 0n
        ? 1n
        : reserve
      : highestBid + (highestBid * BigInt(minIncBps)) / 10_000n + 1n;
    return {seller, collection, endsAt, tokenId, reserve, highestBid, highestBidder, settled, live, hasBid, minNext};
  }, [data]);

  useEffect(() => {
    if (!derived || derived.live || derived.settled || !derived.hasBid) return;
    if (!isConnected) return;
    if (settlePhase === "error") return;
    if (autoSettleAttempted.current || isPending || isConfirming || hash) return;
    autoSettleAttempted.current = true;
    setSettlePhase("awaiting_wallet");
    void settle(aid)
      .then(h => {
        setHash(h as Hex);
        setSettlePhase("done");
      })
      .catch(() => {
        setSettlePhase("error");
      });
  }, [derived, isConnected, isPending, isConfirming, hash, aid, settle, setHash, settlePhase]);

  if (isLoading) return <div className="text-sm text-neutral-400">Loading auction…</div>;
  if (!data || !derived) return <div className="text-sm text-neutral-400">Auction not found.</div>;

  const {seller, collection, endsAt, tokenId, reserve, highestBid, highestBidder, settled, live, hasBid, minNext} =
    derived;

  const needsSettle = !live && !settled && hasBid;

  return (
    <div className="space-y-4 max-w-xl">
      <div>
        <div className="text-xs text-neutral-400 break-all">{collection}</div>
        <h1 className="text-2xl md:text-3xl font-bold">Auction #{aid.toString()} — Token #{tokenId.toString()}</h1>
      </div>
      <div className="border border-neutral-800 rounded p-4 space-y-1 text-sm">
        <div>
          Seller: <span className="break-all">{seller}</span>
        </div>
        <div>Reserve: {formatEther(reserve)} C2FLR</div>
        <div>
          Highest bid: {formatEther(highestBid)} C2FLR{" "}
          {hasBid && (
            <>
              by <span className="break-all">{highestBidder}</span>
            </>
          )}
        </div>
        <div>Ends: {new Date(Number(endsAt) * 1000).toLocaleString()}</div>
        <div>
          Status:{" "}
          <span
            className={settled ? "text-neutral-400" : live ? "text-emerald-400" : "text-yellow-400"}
          >
            {settled ? "Settled" : live ? "Live" : "Awaiting settle"}
          </span>
        </div>
      </div>

      {live && <BidForm id={aid} minNext={minNext} />}

      {needsSettle && !isConnected && (
        <p className="text-sm text-amber-400/90">
          Connect your wallet in the header — we will request a single confirmation to settle this auction automatically.
        </p>
      )}

      {needsSettle && isConnected && (
        <div className="space-y-2">
          <p className="text-sm text-neutral-300">
            {settlePhase === "error"
              ? "Wallet rejected or the transaction failed. You can retry — only a wallet confirmation is required."
              : isPending || settlePhase === "awaiting_wallet"
                ? "Confirm settlement in your wallet — no extra steps in the app."
                : isConfirming
                  ? "Settlement confirming on-chain…"
                  : isConfirmed
                    ? "Auction settled."
                    : "Preparing settlement…"}
          </p>
          {settlePhase === "error" && (
            <button
              type="button"
              className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
              disabled={isPending || isConfirming}
              onClick={async () => {
                setSettlePhase("awaiting_wallet");
                try {
                  const h = await settle(aid);
                  setHash(h as Hex);
                  setSettlePhase("done");
                } catch {
                  setSettlePhase("error");
                }
              }}
            >
              {isPending ? "Confirm in wallet…" : "Retry settlement"}
            </button>
          )}
          <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={error} label="Settle" />
        </div>
      )}

      {!live && !hasBid && !settled && <div className="text-sm text-neutral-400">Auction ended with no bids.</div>}
    </div>
  );
}
