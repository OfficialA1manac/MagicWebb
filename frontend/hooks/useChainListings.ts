"use client";
import {useQuery} from "@tanstack/react-query";
import {type Address} from "viem";
import {api} from "@/lib/api";
import {useRealtime} from "./useRealtime";

// Shape the existing UI expects (matches ActiveListing from marketIndex.ts).
export type ListingRow = {
  coll: Address;
  id: bigint;
  price: bigint;
  expiresAt: bigint;
  seller: Address;
  name?: string;
  imageUri?: string;
};

// Raw shape returned by the Go backend JSON.
type ApiListing = {
  collection: string;
  token_id: string | number;
  seller: string;
  price_wei: string;
  expires_at: number;
  name?: string;
  image_uri?: string;
};

function mapApiListing(l: ApiListing): ListingRow {
  return {
    coll: l.collection as Address,
    id: BigInt(l.token_id),
    price: BigInt(l.price_wei),
    expiresAt: BigInt(l.expires_at),
    seller: l.seller as Address,
    name: l.name,
    imageUri: l.image_uri,
  };
}

export function useChainListings() {
  useRealtime(["listings"]);

  return useQuery({
    queryKey: ["chain-listings"],
    queryFn: async () => {
      const raw = (await api.getListings({limit: 200})) as ApiListing[];
      const listings = raw.map(mapApiListing);
      // Build a meta map keyed by lowercase collection address.
      const meta: Record<string, {name: string; symbol: string}> = {};
      // The backend listing rows include name but not symbol — leave symbol empty.
      for (const l of listings) {
        const key = l.coll.toLowerCase();
        if (!meta[key] && l.name) {
          meta[key] = {name: l.name, symbol: ""};
        }
      }
      return {listings, meta};
    },
    staleTime: 30_000,
    // No refetchInterval — invalidated by useRealtime on Listed/Bought/Cancelled events.
  });
}
