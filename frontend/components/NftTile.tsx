"use client";
import {useState} from "react";
import Link from "next/link";
import {formatEther, type Address} from "viem";
import {FavoriteToggle} from "./FavoriteToggle";
import {useTokenImage} from "@/hooks/useTokenImage";
import {CURRENCY_SYMBOL} from "@/lib/addresses";

function NftImage({src, id, alt}: {src?: string | null; id: bigint; alt: string}) {
  const [err, setErr] = useState(false);
  if (src && !err) {
    return (
      <img
        src={src}
        alt={alt}
        className="w-full aspect-square object-cover"
        onError={() => setErr(true)}
      />
    );
  }
  return (
    <div className="w-full aspect-square flex items-center justify-center bg-neutral-800 text-3xl font-mono text-neutral-600 select-none">
      #{id.toString()}
    </div>
  );
}

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
  const {data: imageUrl} = useTokenImage(coll, id);

  return (
    <div className="relative rounded-xl border border-neutral-800 bg-neutral-900/40 overflow-hidden transition hover:border-neutral-600 hover:bg-neutral-900/60">
      {showFavorite && (
        <div className="absolute right-2 top-2 z-10">
          <FavoriteToggle coll={coll} id={id} />
        </div>
      )}
      <Link href={`/token/${coll}/${id.toString()}`} className="block">
        <NftImage src={imageUrl} id={id} alt={`${title} #${id}`} />
        <div className="p-3">
          <div className="text-xs text-neutral-500 truncate">
            {title}<span className="font-normal text-neutral-600">{sym}</span>
          </div>
          <div className="mt-0.5 text-sm font-mono font-semibold text-neutral-100">#{id.toString()}</div>
          {priceWei !== undefined && (
            <div className="mt-1.5 text-sm font-medium text-emerald-400">{formatEther(priceWei)} {CURRENCY_SYMBOL}</div>
          )}
        </div>
      </Link>
    </div>
  );
}
