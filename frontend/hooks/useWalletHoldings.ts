"use client";
import {useQuery} from "@tanstack/react-query";
import {type Address} from "viem";
import {usePublicClient} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {fetchActiveErc721Listings, fetchCollectionMeta} from "@/lib/marketIndex";
import {readTrackedCollectionAddresses} from "@/lib/trackedCollections";
import {readFavoritesFromStorage} from "@/lib/favorites";
import {fetchWalletErc721Holdings} from "@/lib/walletNfts";

export function useWalletHoldings(owner: Address | undefined, favoritesKey: string) {
  const client = usePublicClient();

  return useQuery({
    queryKey: ["wallet-holdings", owner, ADDR.marketplace, favoritesKey],
    queryFn: async () => {
      if (!client || !owner) throw new Error("missing");

      // Build collection set from listings + tracked + favorites in parallel.
      const [listings, tracked, favs] = await Promise.all([
        fetchActiveErc721Listings(client, ADDR.marketplace),
        Promise.resolve(readTrackedCollectionAddresses()),
        Promise.resolve(readFavoritesFromStorage()),
      ]);

      const set = new Set<string>();
      for (const l of listings) set.add(l.coll.toLowerCase());
      for (const t of tracked)  set.add(t.toLowerCase());
      for (const f of favs)     set.add(f.coll.toLowerCase());

      const collections = [...set].map(a => a as Address);
      const tokens = await fetchWalletErc721Holdings(client, owner, collections);
      const metaColls = [...new Set(tokens.map(t => t.coll))];
      const meta = await fetchCollectionMeta(client, metaColls);
      return {tokens, meta};
    },
    enabled: !!client && !!owner,
    staleTime: 30_000,
  });
}
