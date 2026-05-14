"use client";
import {useParams} from "next/navigation";
import {useState} from "react";
import {useAccount, useReadContract} from "wagmi";
import {formatEther, type Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi, ERC721Abi} from "@/lib/abi";
import {BuyButton} from "@/components/BuyButton";
import {OfferModal} from "@/components/OfferModal";
import {OwnerActions} from "@/components/OwnerActions";
import {FavoriteToggle} from "@/components/FavoriteToggle";

export default function TokenPage() {
  const {addr, id} = useParams<{addr: string; id: string}>();
  const {address} = useAccount();
  const coll = addr as Address;
  const tokenId = BigInt(id);
  const [showOffer, setShowOffer] = useState(false);

  const {data: owner, isLoading: ownerLoading} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "ownerOf", args: [tokenId]
  });
  const {data: listing} = useReadContract({
    address: ADDR.marketplace, abi: MarketplaceAbi, functionName: "listings", args: [coll, tokenId]
  });

  const [seller, expiresAt, , price] = (listing as [Address, bigint, number, bigint, bigint] | undefined) ??
    ["0x0000000000000000000000000000000000000000" as Address, 0n, 0, 0n, 0n];
  const isListed = seller !== "0x0000000000000000000000000000000000000000";
  const isOwner = !!address && address.toLowerCase() === (owner as string | undefined)?.toLowerCase();
  const expired = isListed && expiresAt > 0n && BigInt(Math.floor(Date.now() / 1000)) > expiresAt;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-6 md:gap-8">
      <div className="aspect-square bg-neutral-900 rounded-lg flex items-center justify-center text-5xl md:text-6xl text-neutral-700">
        #{tokenId.toString()}
      </div>
      <div className="space-y-4">
        <div>
          <div className="text-xs text-neutral-400 break-all">{coll}</div>
          <div className="mt-1 flex flex-wrap items-center gap-3">
            <h1 className="text-2xl md:text-3xl font-bold">Token #{tokenId.toString()}</h1>
            <FavoriteToggle coll={coll} id={tokenId} />
          </div>
          <div className="text-sm text-neutral-400 mt-1">
            Owner: {ownerLoading ? "…" : <span className="break-all">{(owner as string) ?? "—"}</span>}
          </div>
        </div>

        {isListed && (
          <div className="border border-neutral-800 rounded p-4 space-y-2">
            <div className="text-sm text-neutral-400">Listed at</div>
            <div className="text-2xl font-mono">{formatEther(price)} C2FLR</div>
            <div className="text-xs text-neutral-500">
              {expired ? "Expired " : "Expires "}{new Date(Number(expiresAt) * 1000).toLocaleString()}
            </div>
            {!isOwner && <BuyButton coll={coll} id={tokenId} price={price} expiresAt={expiresAt} />}
          </div>
        )}

        {isOwner ? (
          <OwnerActions coll={coll} tokenId={tokenId} isListed={isListed} />
        ) : (
          <button
            className="w-full sm:w-auto px-4 py-2 rounded border border-neutral-700 hover:border-neutral-500"
            onClick={() => setShowOffer(true)}
          >Make offer</button>
        )}
        {showOffer && <OfferModal coll={coll} tokenId={tokenId} onClose={() => setShowOffer(false)} />}
      </div>
    </div>
  );
}
