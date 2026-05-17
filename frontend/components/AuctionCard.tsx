"use client";
import Link from "next/link";
import {formatEther, type Address} from "viem";
import {CURRENCY_SYMBOL} from "@/lib/addresses";

export function AuctionCard({
  id, coll, tokenId, highestBid, endsAt
}: {id: bigint; coll: Address; tokenId: bigint; highestBid: bigint; endsAt: bigint}) {
  const ends = new Date(Number(endsAt) * 1000);
  return (
    <Link
      href={`/auction/${id.toString()}`}
      className="block rounded-lg border border-neutral-800 p-4 hover:border-neutral-600"
    >
      <div className="text-xs text-neutral-400 break-all">{coll}</div>
      <div className="text-lg font-mono mt-1">#{tokenId.toString()}</div>
      <div className="mt-2 text-sm">High bid: {formatEther(highestBid)} {CURRENCY_SYMBOL}</div>
      <div className="text-xs text-neutral-400">Ends {ends.toLocaleString()}</div>
    </Link>
  );
}
