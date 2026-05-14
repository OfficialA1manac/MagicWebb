"use client";
import Link from "next/link";
import {formatEther, type Address} from "viem";
import {FavoriteToggle} from "./FavoriteToggle";

export function NftTile({
  coll,
  id,
  priceWei,
  collectionName,
  symbol,
  showFavorite
}: {
  coll: Address;
  id: bigint;
  priceWei?: bigint;
  collectionName?: string;
  symbol?: string;
  showFavorite?: boolean;
}) {
  const title = collectionName ?? "Collection";
  const sym = symbol ? ` · ${symbol}` : "";
  return (
    <div className="relative rounded-xl border border-neutral-800 bg-neutral-900/40 overflow-hidden transition hover:border-neutral-600 hover:bg-neutral-900/60">
      {showFavorite && (
        <div className="absolute right-2 top-2 z-10">
          <FavoriteToggle coll={coll} id={id} />
        </div>
      )}
      <Link href={`/token/${coll}/${id.toString()}`} className="block p-4">
        <div className="text-xs text-neutral-500 break-all line-clamp-2">{coll}</div>
        <div className="mt-1 text-sm font-semibold text-neutral-100 line-clamp-2">
          {title}
          <span className="font-normal text-neutral-500">{sym}</span>
        </div>
        <div className="mt-2 text-2xl font-mono text-neutral-200">#{id.toString()}</div>
        {priceWei !== undefined && (
          <div className="mt-3 text-sm font-medium text-emerald-400">{formatEther(priceWei)} C2FLR</div>
        )}
      </Link>
    </div>
  );
}
