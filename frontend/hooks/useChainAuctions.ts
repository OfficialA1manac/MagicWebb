"use client";
import {useQuery} from "@tanstack/react-query";
import {type Address, zeroAddress} from "viem";
import {usePublicClient} from "wagmi";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi/AuctionHouse";
import {fetchCollectionMeta} from "@/lib/marketIndex";
import {useRealtime} from "./useRealtime";
import {useServerTime} from "./useServerTime";

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

/**
 * Parses the `AuctionHouse.auctions` public getter return tuple.
 * Field order matches the DEPLOYED (v1) struct — 12 fields:
 * 0:seller 1:startsAt 2:minIncrementBps 3:settled 4:standard
 * 5:collection 6:endsAt 7:tokenId 8:reserve 9:highestBid
 * 10:highestBidder 11:amount
 * Update to 13 fields after redeployment with originalEndsAt.
 */
function parseAuctionTuple(
  id: bigint,
  row: readonly [
    Address,  // 0  seller
    bigint,   // 1  startsAt
    number,   // 2  minIncrementBps
    boolean,  // 3  settled
    number,   // 4  standard
    Address,  // 5  collection
    bigint,   // 6  endsAt
    bigint,   // 7  tokenId
    bigint,   // 8  reserve
    bigint,   // 9  highestBid
    Address,  // 10 highestBidder
    bigint,   // 11 amount
  ]
): AuctionRow | null {
  const [seller, startsAt, , settled, , collection, endsAt, tokenId, reserve, highestBid, highestBidder] = row;
  if (seller === zeroAddress) return null;
  return {id, seller, startsAt, endsAt, settled, collection, tokenId, reserve, highestBid, highestBidder};
}

async function fetchAllAuctionRows(client: NonNullable<ReturnType<typeof usePublicClient>>) {
  const nextId = await client.readContract({
    address: ADDR.auction,
    abi: AuctionHouseAbi,
    functionName: "nextAuctionId",
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
        args: [BigInt(id)] as const,
      });
    }
    const results = await client.multicall({contracts, allowFailure: true});
    for (let j = 0; j < results.length; j++) {
      const r = results[j];
      if (r.status !== "success") continue;
      const parsed = parseAuctionTuple(
        BigInt(start + j),
        r.result as Parameters<typeof parseAuctionTuple>[1]
      );
      if (parsed) rows.push(parsed);
    }
  }
  return rows;
}

export function useChainAuctions() {
  useRealtime(["auction:*"]);
  const client = usePublicClient();
  const {nowSeconds} = useServerTime();

  return useQuery({
    queryKey: ["chain-auctions", ADDR.auction],
    queryFn: async () => {
      if (!client) throw new Error("No RPC client");
      const all = await fetchAllAuctionRows(client);
      const now = nowSeconds();
      const open   = all.filter(a => !a.settled && now < a.endsAt);
      const closed = all.filter(a => a.settled  || now >= a.endsAt);
      const colls  = [...new Set(all.map(a => a.collection))];
      const meta   = await fetchCollectionMeta(client, colls);
      return {all, open, closed, now, meta};
    },
    enabled: !!client,
    staleTime: 30_000,
    // No refetchInterval — invalidated by useRealtime on bid/settle events.
  });
}
