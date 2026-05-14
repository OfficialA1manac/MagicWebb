"use client";
import {useParams} from "next/navigation";
import {useReadContract} from "wagmi";
import {formatEther, type Address, type Hex} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";
import {BidForm} from "@/components/BidForm";
import {TxBanner} from "@/components/TxBanner";
import {useSettleAuction} from "@/hooks/useSettleAuction";
import {useTx} from "@/hooks/useTx";

type Auction = readonly [
  Address, bigint, number, boolean,
  Address, bigint, bigint,
  bigint, bigint, Address
];

export default function AuctionPage() {
  const {id} = useParams<{id: string}>();
  const aid = BigInt(id);
  const {data, isLoading} = useReadContract({
    address: ADDR.auction, abi: AuctionHouseAbi, functionName: "auctions", args: [aid]
  });
  const {settle, isPending, error} = useSettleAuction();
  const {hash, setHash, isConfirming, isConfirmed} = useTx();

  if (isLoading) return <div className="text-sm text-neutral-400">Loading auction…</div>;
  if (!data) return <div className="text-sm text-neutral-400">Auction not found.</div>;
  const a = data as Auction;
  const [seller, , minIncBps, settled, collection, endsAt, tokenId, reserve, highestBid, highestBidder] = a;
  const live = !settled && BigInt(Math.floor(Date.now() / 1000)) < endsAt;
  const hasBid = highestBidder !== "0x0000000000000000000000000000000000000000";
  const minNext = !hasBid
    ? (reserve === 0n ? 1n : reserve)
    : highestBid + (highestBid * BigInt(minIncBps)) / 10_000n + 1n;

  return (
    <div className="space-y-4 max-w-xl">
      <div>
        <div className="text-xs text-neutral-400 break-all">{collection}</div>
        <h1 className="text-2xl md:text-3xl font-bold">Auction #{aid.toString()} — Token #{tokenId.toString()}</h1>
      </div>
      <div className="border border-neutral-800 rounded p-4 space-y-1 text-sm">
        <div>Seller: <span className="break-all">{seller}</span></div>
        <div>Reserve: {formatEther(reserve)} C2FLR</div>
        <div>Highest bid: {formatEther(highestBid)} C2FLR {hasBid && <>by <span className="break-all">{highestBidder}</span></>}</div>
        <div>Ends: {new Date(Number(endsAt) * 1000).toLocaleString()}</div>
        <div>Status: <span className={settled ? "text-neutral-400" : live ? "text-emerald-400" : "text-yellow-400"}>{settled ? "Settled" : live ? "Live" : "Awaiting settle"}</span></div>
      </div>

      {live && <BidForm id={aid} minNext={minNext} />}

      {!live && !settled && hasBid && (
        <div className="space-y-2">
          <button
            className="w-full sm:w-auto px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
            disabled={isPending || isConfirming}
            onClick={async () => {
              const h = await settle(aid);
              setHash(h as Hex);
            }}
          >{isPending ? "Confirm in wallet…" : isConfirming ? "Settling…" : "Settle auction"}</button>
          <TxBanner hash={hash} isConfirming={isConfirming} isConfirmed={isConfirmed} error={error} label="Settle" />
        </div>
      )}
      {!live && !hasBid && !settled && (
        <div className="text-sm text-neutral-400">Auction ended with no bids.</div>
      )}
    </div>
  );
}
