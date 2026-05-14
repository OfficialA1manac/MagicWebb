"use client";
import {useDeferredValue, useMemo, useState} from "react";
import {useRouter} from "next/navigation";
import {isAddress} from "viem";
import {usePublicClient} from "wagmi";
import {useQuery} from "@tanstack/react-query";
import {useChainListings} from "@/hooks/useChainListings";
import {NftTile} from "@/components/NftTile";
import {shuffleInPlace, fetchCollectionMeta} from "@/lib/marketIndex";
import {readTrackedCollectionAddresses} from "@/lib/trackedCollections";

export default function Search() {
  const router = useRouter();
  const client = usePublicClient();
  const [raw, setRaw] = useState("");
  const dq = useDeferredValue(raw.trim());

  const {data, isLoading, error, refetch} = useChainListings();
  const tracked = useMemo(() => readTrackedCollectionAddresses(), []);

  const trackedMeta = useQuery({
    queryKey: ["search-tracked-meta", tracked.map(t => t.toLowerCase()).join(",")],
    queryFn: async () => {
      if (!client || tracked.length === 0) return {};
      return fetchCollectionMeta(client, tracked);
    },
    enabled: !!client && tracked.length > 0
  });

  const mergedMeta = useMemo(() => {
    return {...(data?.meta ?? {}), ...(trackedMeta.data ?? {})};
  }, [data?.meta, trackedMeta.data]);

  const shuffled = useMemo(() => {
    if (!data?.listings?.length) return [];
    const copy = [...data.listings];
    shuffleInPlace(copy);
    return copy;
  }, [data?.listings]);

  const filtered = useMemo(() => {
    const q = dq.toLowerCase();
    if (!q) return shuffled;
    return shuffled.filter(l => {
      const key = l.coll.toLowerCase();
      const m = mergedMeta[key];
      const name = (m?.name ?? "").toLowerCase();
      const sym = (m?.symbol ?? "").toLowerCase();
      return key.includes(q) || name.includes(q) || sym.includes(q);
    });
  }, [shuffled, dq, mergedMeta]);

  const goCollection = () => {
    const t = raw.trim();
    if (isAddress(t)) router.push(`/collection/${t}`);
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl md:text-3xl font-bold">Search and discover</h1>
        <p className="mt-2 max-w-2xl text-sm text-neutral-400">
          Live ERC-721 listings are read from the Marketplace contract (recent <code className="text-neutral-300">Listed</code>{" "}
          logs + on-chain state). Filter by collection name, symbol, or address. Optional{" "}
          <span className="font-mono text-xs">NEXT_PUBLIC_TRACKED_COLLECTIONS</span> adds projects that have not listed yet.
        </p>
      </div>

      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <input
          className="min-w-0 flex-1 rounded-lg border border-neutral-700 bg-neutral-900 px-3 py-2.5 text-sm"
          placeholder="Name, symbol, or 0x collection address…"
          value={raw}
          onChange={e => setRaw(e.target.value)}
          onKeyDown={e => {
            if (e.key === "Enter" && isAddress(raw.trim())) goCollection();
          }}
        />
        <button
          type="button"
          className="rounded-lg bg-emerald-600 px-4 py-2.5 text-sm font-medium text-neutral-950 hover:bg-emerald-500 disabled:opacity-40"
          disabled={!isAddress(raw.trim())}
          onClick={goCollection}
        >
          Open collection
        </button>
        <button
          type="button"
          className="rounded-lg border border-neutral-600 px-4 py-2.5 text-sm hover:border-emerald-500/50"
          onClick={() => refetch()}
        >
          Refresh index
        </button>
      </div>

      {error && (
        <div className="rounded-lg border border-red-900/50 bg-red-950/30 px-4 py-3 text-sm text-red-200">
          {String((error as Error).message)}
        </div>
      )}

      {isLoading && <div className="text-sm text-neutral-500">Loading marketplace index…</div>}

      {!isLoading && !error && filtered.length === 0 && (
        <div className="rounded-xl border border-dashed border-neutral-700 p-8 text-center text-sm text-neutral-500">
          {data?.listings.length === 0
            ? "No active ERC-721 listings found on this Marketplace. List a token from the List NFT page."
            : "No listings match your filter."}
        </div>
      )}

      {filtered.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {filtered.map(l => {
            const m = mergedMeta[l.coll.toLowerCase()];
            return (
              <NftTile
                key={`${l.coll}-${l.id}`}
                coll={l.coll}
                id={l.id}
                priceWei={l.price}
                collectionName={m?.name}
                symbol={m?.symbol}
                showFavorite
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
