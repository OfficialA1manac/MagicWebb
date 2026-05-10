"use client";
import {useParams} from "next/navigation";
import {useState} from "react";
import {useAccount, useReadContract} from "wagmi";
import {formatEther, type Address} from "viem";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi, ERC721Abi} from "@/lib/abi";
import {BuyButton} from "@/components/BuyButton";
import {OfferModal} from "@/components/OfferModal";

export default function TokenPage() {
  const {addr, id} = useParams<{addr: string; id: string}>();
  const {address} = useAccount();
  const coll = addr as Address;
  const tokenId = BigInt(id);
  const [showOffer, setShowOffer] = useState(false);

  const {data: owner} = useReadContract({
    address: coll, abi: ERC721Abi, functionName: "ownerOf", args: [tokenId]
  });
  const {data: listing} = useReadContract({
    address: ADDR.marketplace, abi: MarketplaceAbi, functionName: "listings", args: [coll, tokenId]
  });

  const [seller, expiresAt, price] = (listing as [Address, bigint, bigint] | undefined) ?? ["0x0000000000000000000000000000000000000000" as Address, 0n, 0n];
  const isListed = seller !== "0x0000000000000000000000000000000000000000";
  const isOwner = address?.toLowerCase() === (owner as string | undefined)?.toLowerCase();

  return (
    <div className="grid md:grid-cols-2 gap-8">
      <div className="aspect-square bg-neutral-900 rounded-lg flex items-center justify-center text-6xl text-neutral-700">
        #{tokenId.toString()}
      </div>
      <div className="space-y-4">
        <div>
          <div className="text-xs text-neutral-400 break-all">{coll}</div>
          <h1 className="text-2xl font-bold">Token #{tokenId.toString()}</h1>
          <div className="text-sm text-neutral-400 mt-1">Owner: <span className="break-all">{(owner as string) ?? "—"}</span></div>
        </div>

        {isListed ? (
          <div className="border border-neutral-800 rounded p-4 space-y-2">
            <div className="text-sm text-neutral-400">Listed at</div>
            <div className="text-2xl font-mono">{formatEther(price)} C2FLR</div>
            <div className="text-xs text-neutral-500">Expires {new Date(Number(expiresAt) * 1000).toLocaleString()}</div>
            {!isOwner && <BuyButton coll={coll} id={tokenId} price={price} />}
          </div>
        ) : (
          <div className="text-sm text-neutral-400">Not listed.</div>
        )}

        {!isOwner && (
          <button
            className="px-4 py-2 rounded border border-neutral-700 hover:border-neutral-500"
            onClick={() => setShowOffer(true)}
          >Make offer</button>
        )}
        {showOffer && <OfferModal coll={coll} tokenId={tokenId} onClose={() => setShowOffer(false)} />}
      </div>
    </div>
  );
}
