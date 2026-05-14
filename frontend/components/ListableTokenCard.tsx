"use client";
import {type Address, zeroAddress} from "viem";
import {useReadContract} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {MarketplaceAbi} from "@/lib/abi";
import {NftTile} from "@/components/NftTile";
import {OwnerActions} from "@/components/OwnerActions";

export function ListableTokenCard({
  coll,
  id,
  collectionName,
  symbol,
  defaultOpen
}: {
  coll: Address;
  id: bigint;
  collectionName?: string;
  symbol?: string;
  defaultOpen?: "list" | "auction";
}) {
  const {data: listing} = useReadContract({
    address: ADDR.marketplace,
    abi: MarketplaceAbi,
    functionName: "listings",
    args: [coll, id]
  });

  const row = listing as [Address, bigint, number, bigint, bigint] | undefined;
  const seller = row?.[0] ?? zeroAddress;
  const isListed = seller !== zeroAddress;

  return (
    <div className="overflow-hidden rounded-2xl border border-neutral-800 bg-neutral-900/30">
      <NftTile coll={coll} id={id} collectionName={collectionName} symbol={symbol} showFavorite />
      <div className="border-t border-neutral-800 p-4">
        <OwnerActions coll={coll} tokenId={id} isListed={isListed} defaultTab={defaultOpen ?? null} />
      </div>
    </div>
  );
}
