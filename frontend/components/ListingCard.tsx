"use client";
import Link from "next/link";
import {formatEther, type Address} from "viem";

export function ListingCard({coll, id, price}: {coll: Address; id: bigint; price: bigint}) {
  return (
    <Link
      href={`/token/${coll}/${id.toString()}`}
      className="block rounded-lg border border-neutral-800 p-4 hover:border-neutral-600"
    >
      <div className="text-xs text-neutral-400 break-all">{coll}</div>
      <div className="text-lg font-mono mt-1">#{id.toString()}</div>
      <div className="mt-2 text-sm">{formatEther(price)} C2FLR</div>
    </Link>
  );
}
