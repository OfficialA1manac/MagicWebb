"use client";
import {useState} from "react";
import {useReadContract} from "wagmi";
import Link from "next/link";
import {formatEther, type Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

type Auction = readonly [
  Address, bigint, number, boolean,
  Address, bigint, bigint,
  bigint, bigint, Address
];

function AuctionRow({id}: {id: bigint}) {
  const {data, isLoading} = useReadContract({
    address: ADDR.auction, abi: AuctionHouseAbi, functionName: "auctions", args: [id]
  });
  if (isLoading) return <div className="border border-neutral-800 rounded p-3 text-sm text-neutral-500 animate-pulse">Loading…</div>;
  if (!data) return null;
  const a = data as Auction;
  const [seller, , , settled, collection, endsAt, tokenId, , highestBid] = a;
  if (seller === "0x0000000000000000000000000000000000000000") return null;
  const ends = new Date(Number(endsAt) * 1000);
  const live = !settled && BigInt(Math.floor(Date.now() / 1000)) < endsAt;
  return (
    <Link href={`/auction/${id.toString()}`}
      className="block border border-neutral-800 rounded p-3 hover:border-neutral-600">
      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="truncate">
          <div className="font-mono">#{id.toString()} · token {tokenId.toString()}</div>
          <div className="text-xs text-neutral-500 truncate">{collection}</div>
        </div>
        <div className="text-right shrink-0">
          <div>{formatEther(highestBid)} C2FLR</div>
          <div className={`text-xs ${live ? "text-emerald-400" : settled ? "text-neutral-500" : "text-yellow-400"}`}>
            {settled ? "Settled" : live ? `Ends ${ends.toLocaleDateString()}` : "Awaiting settle"}
          </div>
        </div>
      </div>
    </Link>
  );
}

export default function AuctionsPage() {
  const {data: nextId} = useReadContract({
    address: ADDR.auction, abi: AuctionHouseAbi, functionName: "nextAuctionId"
  });
  const [page, setPage] = useState(0);
  const total = Number((nextId as bigint | undefined) ?? 0n);
  const pageSize = 20;
  const start = Math.max(1, total - (page + 1) * pageSize);
  const end = total - page * pageSize;
  const ids: bigint[] = [];
  for (let i = end - 1; i >= start; i--) ids.push(BigInt(i));

  return (
    <div className="space-y-4">
      <h1 className="text-2xl md:text-3xl font-bold">Auctions</h1>
      <div className="text-sm text-neutral-400">{total === 0 ? "No auctions yet." : `${total} auctions on-chain. Showing most recent.`}</div>
      <div className="space-y-2">
        {ids.map(i => <AuctionRow key={i.toString()} id={i} />)}
      </div>
      {total > pageSize && (
        <div className="flex gap-2 text-sm">
          <button className="px-3 py-1 rounded border border-neutral-700 disabled:opacity-40"
            disabled={page === 0} onClick={() => setPage(p => p - 1)}>Newer</button>
          <button className="px-3 py-1 rounded border border-neutral-700 disabled:opacity-40"
            disabled={end - pageSize <= 1} onClick={() => setPage(p => p + 1)}>Older</button>
        </div>
      )}
    </div>
  );
}
