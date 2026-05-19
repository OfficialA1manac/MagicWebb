"use client";
import {useQuery} from "@tanstack/react-query";
import {type Address} from "viem";
import {api} from "@/lib/api";
import {useRealtime} from "./useRealtime";
import {useServerTime} from "./useServerTime";

// Shape the UI expects — bigint fields match original on-chain ABI types.
export type AuctionRow = {
  id: bigint;
  seller: Address;
  startsAt: bigint;
  endsAt: bigint;
  settled: boolean;
  collection: Address;
  tokenId: bigint;
  reserve: bigint;
  highestBid: bigint;
  highestBidder: Address;
};

// Raw shape returned by the Go backend JSON.
type ApiAuction = {
  auction_id: string | number;
  collection: string;
  token_id: string | number;
  seller: string;
  reserve_price_wei: string;
  highest_bid_wei: string;
  highest_bidder: string;
  starts_at: number;
  ends_at: number;
  status: string;
};

type ApiCollection = {
  address: string;
  name: string;
  symbol: string;
};

function mapApiAuction(a: ApiAuction): AuctionRow {
  return {
    id: BigInt(a.auction_id),
    seller: a.seller as Address,
    startsAt: BigInt(a.starts_at),
    endsAt: BigInt(a.ends_at),
    settled: a.status === "settled",
    collection: a.collection as Address,
    tokenId: BigInt(a.token_id),
    reserve: BigInt(a.reserve_price_wei),
    highestBid: BigInt(a.highest_bid_wei),
    highestBidder: a.highest_bidder as Address,
  };
}

export function useChainAuctions() {
  useRealtime(["auction:*"]);
  const {nowSeconds} = useServerTime();

  return useQuery({
    queryKey: ["chain-auctions"],
    queryFn: async () => {
      const [rawAuctions, rawColls] = await Promise.all([
        api.getAuctions({limit: 200}) as Promise<ApiAuction[]>,
        api.getCollections(200) as Promise<ApiCollection[]>,
      ]);

      const all = rawAuctions.map(mapApiAuction);
      const now = nowSeconds();
      const open   = all.filter(a => !a.settled && now < a.endsAt);
      const closed = all.filter(a => a.settled  || now >= a.endsAt);

      const meta: Record<string, {name: string; symbol: string}> = {};
      for (const c of rawColls) {
        meta[c.address.toLowerCase()] = {name: c.name, symbol: c.symbol};
      }

      return {all, open, closed, now, meta};
    },
    staleTime: 30_000,
    // No refetchInterval — invalidated by useRealtime on bid/settle events.
  });
}
