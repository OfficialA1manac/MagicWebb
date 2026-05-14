"use client";
import {useQuery} from "@tanstack/react-query";
import {usePublicClient} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {fetchActiveErc721Listings, fetchCollectionMeta} from "@/lib/marketIndex";

export function useChainListings() {
  const client = usePublicClient();
  return useQuery({
    queryKey: ["chain-listings", ADDR.marketplace],
    queryFn: async () => {
      if (!client) throw new Error("No RPC client");
      const listings = await fetchActiveErc721Listings(client, ADDR.marketplace);
      const colls = [...new Set(listings.map(l => l.coll))];
      const meta = await fetchCollectionMeta(client, colls);
      return {listings, meta};
    },
    enabled: !!client,
    staleTime: 25_000
  });
}
