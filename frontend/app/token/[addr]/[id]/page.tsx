"use client";
import {useParams} from "next/navigation";
import {useState} from "react";
import {useAccount, useReadContract} from "wagmi";
import {formatEther, type Address} from "viem";
import {ADDR, EXPLORER_URL, CURRENCY_SYMBOL} from "@/lib/addresses";
import {MarketplaceAbi, ERC721Abi} from "@/lib/abi";
import {BuyButton} from "@/components/BuyButton";
import {OfferModal} from "@/components/OfferModal";
import {OwnerActions} from "@/components/OwnerActions";
import {FavoriteToggle} from "@/components/FavoriteToggle";
import {useTokenImage} from "@/hooks/useTokenImage";

function TokenImage({src, id, name}: {src?: string | null; id: bigint; name?: string}) {
  const [err, setErr] = useState(false);
  if (src && !err) {
    return (
      <img
        src={src}
        alt={name ?? `Token #${id}`}
        className="w-full aspect-square rounded-xl object-cover"
        onError={() => setErr(true)}
      />
    );
  }
  return (
    <div className="w-full aspect-square rounded-xl bg-neutral-900 flex items-center justify-center text-5xl md:text-6xl text-neutral-700 font-mono select-none">
      #{id.toString()}
    </div>
  );
}

export default function TokenPage() {
  const {addr, id} = useParams<{addr: string; id: string}>();
  const {address} = useAccount();
  const coll = addr as Address;
  const tokenId = BigInt(id);
  const [showOffer, setShowOffer] = useState(false);

  const {data: imageUrl} = useTokenImage(coll, tokenId);

  const {data: owner, isLoading: ownerLoading} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "ownerOf", args: [tokenId]
  });
  const {data: collName} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "name"
  });
  const {data: listing, refetch: refetchListing} = useReadContract({
    address: ADDR.marketplace, abi: MarketplaceAbi, functionName: "listings", args: [coll, tokenId],
    query: {refetchInterval: 15_000}
  });

  const [seller, expiresAt, , price] = (listing as [Address, bigint, number, bigint, bigint] | undefined) ??
    ["0x0000000000000000000000000000000000000000" as Address, 0n, 0, 0n, 0n];
  const isListed = seller !== "0x0000000000000000000000000000000000000000";
  const isOwner = !!address && address.toLowerCase() === (owner as string | undefined)?.toLowerCase();
  const expired = isListed && expiresAt > 0n && BigInt(Math.floor(Date.now() / 1000)) > expiresAt;

  const displayName = (collName as string | undefined)
    ? `${collName as string} #${tokenId.toString()}`
    : `Token #${tokenId.toString()}`;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-6 md:gap-8">
      <TokenImage src={imageUrl} id={tokenId} name={displayName} />

      <div className="space-y-4">
        <div>
          <div className="text-xs text-neutral-500 break-all font-mono">{coll}</div>
          <div className="mt-1 flex flex-wrap items-center gap-3">
            <h1 className="text-2xl md:text-3xl font-bold">{displayName}</h1>
            <FavoriteToggle coll={coll} id={tokenId} />
          </div>
          <div className="text-sm text-neutral-400 mt-1">
            Owner:{" "}
            {ownerLoading ? (
              "…"
            ) : (
              <span className="break-all font-mono text-xs">{(owner as string) ?? "—"}</span>
            )}
          </div>
        </div>

        {isListed && (
          <div className="border border-neutral-800 rounded-xl p-4 space-y-2 bg-neutral-900/30">
            <div className="text-sm text-neutral-400">Listed at</div>
            <div className="text-2xl font-mono font-bold">{formatEther(price)} {CURRENCY_SYMBOL}</div>
            <div className="text-xs text-neutral-500">
              {expired ? "⚠ Expired " : "Expires "}
              {new Date(Number(expiresAt) * 1000).toLocaleString()}
            </div>
            {!isOwner && !expired && (
              <BuyButton coll={coll} id={tokenId} price={price} expiresAt={expiresAt} />
            )}
            {expired && (
              <p className="text-xs text-amber-400">Listing expired — seller needs to relist.</p>
            )}
          </div>
        )}

        {!isListed && !isOwner && (
          <div className="rounded-xl border border-neutral-800 bg-neutral-900/20 p-4 text-sm text-neutral-400">
            Not listed for sale.
          </div>
        )}

        {isOwner ? (
          <OwnerActions coll={coll} tokenId={tokenId} isListed={isListed} onListingChanged={() => void refetchListing()} />
        ) : (
          <button
            type="button"
            className="w-full sm:w-auto px-4 py-2.5 rounded-lg border border-neutral-700 hover:border-emerald-500/50 hover:bg-neutral-800/50 text-sm transition"
            onClick={() => setShowOffer(true)}
          >
            Make offer
          </button>
        )}

        {showOffer && (
          <OfferModal coll={coll} tokenId={tokenId} onClose={() => setShowOffer(false)} />
        )}

        <div className="pt-2 border-t border-neutral-800 text-xs text-neutral-600 space-y-0.5">
          <div>Collection: <span className="font-mono">{coll}</span></div>
          <div>Token ID: <span className="font-mono">{tokenId.toString()}</span></div>
          <a
            href={`${EXPLORER_URL}/token/${coll}/instance/${tokenId}`}
            target="_blank"
            rel="noreferrer"
            className="text-emerald-600 hover:text-emerald-400"
          >
            View on explorer ↗
          </a>
        </div>
      </div>
    </div>
  );
}
