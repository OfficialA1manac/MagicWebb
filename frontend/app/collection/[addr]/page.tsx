"use client";
import {useParams} from "next/navigation";
import {useMemo} from "react";
import {type Address} from "viem";
import {useReadContract, usePublicClient} from "wagmi";
import {useQuery} from "@tanstack/react-query";
import {ERC721Abi} from "@/lib/abi";
import {ADDR, EXPLORER_URL} from "@/lib/addresses";
import {fetchActiveErc721Listings} from "@/lib/marketIndex";
import {NftTile} from "@/components/NftTile";

const SCAN_CAP = 200;

function useCollectionTokens(coll: Address) {
  const client = usePublicClient();
  return useQuery({
    queryKey: ["collection-tokens", coll],
    queryFn: async () => {
      if (!client) throw new Error("no client");
      const ENUM_ID = "0x780e9d63" as `0x${string}`;
      let totalSupply: bigint | undefined;
      try {
        totalSupply = await client.readContract({
          address: coll, abi: ERC721Abi, functionName: "totalSupply", args: []
        }) as bigint;
      } catch { totalSupply = undefined; }

      let isEnum = false;
      try {
        isEnum = await client.readContract({
          address: coll, abi: ERC721Abi, functionName: "supportsInterface", args: [ENUM_ID]
        }) as boolean;
      } catch { isEnum = false; }

      const ids: bigint[] = [];
      const cap = totalSupply !== undefined ? Math.min(Number(totalSupply), SCAN_CAP) : SCAN_CAP;

      if (isEnum && totalSupply !== undefined) {
        const BATCH = 80;
        for (let start = 0; start < cap; start += BATCH) {
          const calls = [];
          for (let i = start; i < Math.min(start + BATCH, cap); i++) {
            calls.push({address: coll, abi: ERC721Abi, functionName: "tokenByIndex" as const, args: [BigInt(i)] as const});
          }
          const res = await client.multicall({contracts: calls, allowFailure: true});
          for (const r of res) if (r.status === "success") ids.push(r.result as bigint);
        }
      } else {
        const BATCH = 60;
        for (let start = 1; start <= cap; start += BATCH) {
          const end = Math.min(start + BATCH - 1, cap);
          const calls = Array.from({length: end - start + 1}, (_, k) => ({
            address: coll, abi: ERC721Abi, functionName: "ownerOf" as const, args: [BigInt(start + k)] as const
          }));
          const res = await client.multicall({contracts: calls, allowFailure: true});
          for (let k = 0; k < res.length; k++) if (res[k].status === "success") ids.push(BigInt(start + k));
        }
      }
      return {ids, totalSupply};
    },
    enabled: !!client,
    staleTime: 60_000,
    refetchInterval: 60_000
  });
}

export default function CollectionPage() {
  const {addr} = useParams<{addr: string}>();
  const coll = addr as Address;
  const client = usePublicClient();

  const {data: nameData} = useReadContract({address: coll, abi: ERC721Abi, functionName: "name"});
  const {data: symbolData} = useReadContract({address: coll, abi: ERC721Abi, functionName: "symbol"});
  const {data: tokensData, isLoading, error} = useCollectionTokens(coll);

  const {data: listingsData} = useQuery({
    queryKey: ["listings-for-coll", coll, ADDR.marketplace],
    queryFn: async () => { if (!client) return []; return fetchActiveErc721Listings(client, ADDR.marketplace); },
    enabled: !!client,
    staleTime: 20_000,
    refetchInterval: 20_000
  });

  const priceMap = useMemo(() => {
    const m = new Map<string, bigint>();
    if (!listingsData) return m;
    for (const l of listingsData) {
      if (l.coll.toLowerCase() === coll.toLowerCase()) m.set(l.id.toString(), l.price);
    }
    return m;
  }, [listingsData, coll]);

  const collName = (nameData as string | undefined) ?? "Collection";
  const sym = (symbolData as string | undefined) ?? "";

  return (
    <div className="space-y-6">
      <div>
        <div className="text-xs text-neutral-500 font-mono break-all">{coll}</div>
        <h1 className="mt-1 text-2xl md:text-3xl font-bold">
          {collName}
          {sym && <span className="ml-2 text-neutral-500 text-lg font-normal">{sym}</span>}
        </h1>
        {tokensData?.totalSupply !== undefined && (
          <p className="mt-1 text-sm text-neutral-500">
            {tokensData.totalSupply.toString()} token{tokensData.totalSupply !== 1n ? "s" : ""} total
            {Number(tokensData.totalSupply) > SCAN_CAP && ` · showing first ${SCAN_CAP}`}
          </p>
        )}
        <a
          href={`${EXPLORER_URL}/token/${coll}`}
          target="_blank" rel="noreferrer"
          className="mt-1 inline-block text-xs text-emerald-600 hover:text-emerald-400"
        >View on explorer ↗</a>
      </div>

      {isLoading && (
        <div className="flex items-center gap-2 text-sm text-neutral-500">
          <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-neutral-600 border-t-emerald-400" />
          Scanning collection…
        </div>
      )}
      {error && <div className="text-sm text-red-400">{(error as Error).message}</div>}
      {!isLoading && tokensData?.ids.length === 0 && (
        <p className="text-sm text-neutral-500">No tokens found in this collection.</p>
      )}
      {tokensData && tokensData.ids.length > 0 && (
        <div className="grid gap-4 grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
          {tokensData.ids.map(id => (
            <NftTile
              key={id.toString()}
              coll={coll} id={id}
              collectionName={collName} symbol={sym}
              priceWei={priceMap.get(id.toString())}
              showFavorite
            />
          ))}
        </div>
      )}
    </div>
  );
}
