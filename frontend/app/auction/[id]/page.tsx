"use client";
import {useParams} from "next/navigation";
import {useReadContract} from "wagmi";
import {formatEther, type Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";
import {BidForm} from "@/components/BidForm";
import {useSettleAuction} from "@/hooks/useSettleAuction";

type Auction = readonly [
  Address, bigint, number, boolean,    // seller, startsAt, minIncBps, settled
  Address, bigint, bigint,              // collection, endsAt, tokenId
  bigint, bigint, Address               // reserve, highestBid, highestBidder
];

export default function AuctionPage() {
  const {id} = useParams<{id: string}>();
  const aid = BigInt(id);
  const {data} = useReadContract({
    address: ADDR.auction, abi: AuctionHouseAbi, functionName: "auctions", args: [aid]
  });
  const {settle, isPending} = useSettleAuction();

  if (!data) return <div className="text-sm text-neutral-400">Loading...</div>;
  const a = data as Auction;
  const [seller, , minIncBps, settled, collection, endsAt, tokenId, reserve, highestBid, highestBidder] = a;
  const live = !settled && BigInt(Math.floor(Date.now() / 1000)) < endsAt;
  const minNext = highestBid === 0n
    ? (reserve === 0n ? 1n : reserve)
    : highestBid + (highestBid * BigInt(minIncBps)) / 10_000n;

  return (
    <div className="space-y-4 max-w-xl">
      <div>
        <div className="text-xs text-neutral-400 break-all">{collection}</div>
        <h1 className="text-2xl font-bold">Auction #{aid.toString()} — Token #{tokenId.toString()}</h1>
      </div>
      <div className="border border-neutral-800 rounded p-4 space-y-1 text-sm">
        <div>Seller: <span className="break-all">{seller}</span></div>
        <div>Reserve: {formatEther(reserve)} C2FLR</div>
        <div>Highest bid: {formatEther(highestBid)} C2FLR {highestBidder !== "0x0000000000000000000000000000000000000000" && <>by <span className="break-all">{highestBidder}</span></>}</div>
        <div>Ends: {new Date(Number(endsAt) * 1000).toLocaleString()}</div>
        <div>Status: {settled ? "Settled" : live ? "Live" : "Awaiting settle"}</div>
      </div>

      {live && <BidForm id={aid} minNext={minNext} />}

      {!live && !settled && highestBidder !== "0x0000000000000000000000000000000000000000" && (
        <button
          className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
          disabled={isPending} onClick={() => settle(aid)}
        >{isPending ? "Settling..." : "Settle auction"}</button>
      )}
    </div>
  );
}
