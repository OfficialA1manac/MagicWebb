"use client";
import {useQuery} from "@tanstack/react-query";
import {type Address, zeroAddress} from "viem";
import {usePublicClient} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi/AuctionHouse";
import {fetchCollectionMeta} from "@/lib/marketIndex";

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

/** Matches `AuctionHouse.auctions` public getter field order (12 values). */
function parseAuctionTuple(
  id: bigint,
  row: readonly [
    Address,
    bigint,
    number,
    boolean,
    number,
    Address,
    bigint,
    bigint,
    bigint,
    bigint,
    Address,
    bigint
  ]
): AuctionRow | null {
  const [seller, startsAt, , settled, , collection, endsAt, tokenId, reserve, highestBid, highestBidder] = row;
  if (seller === zeroAddress) return null;
  return {
    id,
    seller,
    startsAt,
    endsAt,
    settled,
    collection,
    tokenId,
    reserve,
    highestBid,
    highestBidder
  };
}

async function fetchAllAuctionRows(client: NonNullable<ReturnType<typeof usePublicClient>>) {
  const nextId = await client.readContract({
    address: ADDR.auction,
    abi: AuctionHouseAbi,
    functionName: "nextAuctionId"
  });
  const n = Number(nextId);
  const rows: AuctionRow[] = [];
  if (n <= 1) return rows;
  const BATCH = 120;
  for (let start = 1; start < n; start += BATCH) {
    const end = Math.min(n, start + BATCH);
    const contracts = [];
    for (let id = start; id < end; id++) {
      contracts.push({
        address: ADDR.auction,
        abi: AuctionHouseAbi,
        functionName: "auctions" as const,
        args: [BigInt(id)] as const
      });
    }
    const results = await client.multicall({contracts, allowFailure: true});
    for (let j = 0; j < results.length; j++) {
      const r = results[j];
      if (r.status !== "success") continue;
      const parsed = parseAuctionTuple(BigInt(start + j), r.result as Parameters<typeof parseAuctionTuple>[1]);
      if (parsed) rows.push(parsed);
    }
  }
  return rows;
}

export function useChainAuctions() {
  const client = usePublicClient();
  return useQuery({
    queryKey: ["chain-auctions", ADDR.auction],
    queryFn: async () => {
      if (!client) throw new Error("No RPC client");
      const all = await fetchAllAuctionRows(client);
      const now = BigInt(Math.floor(Date.now() / 1000));
      const open = all.filter(a => !a.settled && now < a.endsAt);
      const closed = all.filter(a => a.settled || now >= a.endsAt);
      const colls = [...new Set(all.map(a => a.collection))];
      const meta = await fetchCollectionMeta(client, colls);
      return {all, open, closed, now, meta};
    },
    enabled: !!client,
    staleTime: 15_000
  });
}
