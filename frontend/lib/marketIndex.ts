import {
  type Address,
  type PublicClient,
  zeroAddress,
  parseAbiItem
} from "viem";
import {MarketplaceAbi} from "@/lib/abi/Marketplace";
import {ERC721Abi} from "@/lib/abi/ERC721";
import {indexChunkBlocksEnv, indexFromBlockEnv} from "@/lib/trackedCollections";

const listedEvent = parseAbiItem(
  "event Listed(address indexed coll, uint256 indexed id, address indexed seller, uint8 standard, uint128 amount, uint128 price, uint64 expiresAt)"
);

export type ActiveListing = {
  coll: Address;
  id: bigint;
  price: bigint;
  expiresAt: bigint;
  seller: Address;
};

export type CollectionMeta = {name: string; symbol: string};

async function chunkForwardLogs(
  client: PublicClient,
  params: {address: Address; event: typeof listedEvent},
  fromBlock: bigint,
  chunk: bigint
) {
  const latest = await client.getBlockNumber();
  if (fromBlock > latest) return [];
  const logs: {args?: Record<string, unknown> | null | undefined}[] = [];
  let from = fromBlock;
  while (from <= latest) {
    const to = from + chunk - 1n > latest ? latest : from + chunk - 1n;
    const batch = await client.getLogs({
      ...params,
      fromBlock: from,
      toBlock: to
    });
    logs.push(...batch);
    from = to + 1n;
  }
  return logs;
}

function uniqueListedPairs(logs: readonly {args?: Record<string, unknown> | null | undefined}[]): {
  coll: Address;
  id: bigint;
}[] {
  const m = new Map<string, {coll: Address; id: bigint}>();
  for (const log of logs) {
    const a = log.args;
    if (!a?.coll || a.id === undefined) continue;
    const coll = a.coll as Address;
    const id = BigInt(a.id as bigint);
    m.set(`${coll}-${id}`, {coll, id});
  }
  return [...m.values()];
}

export async function fetchActiveErc721Listings(
  client: PublicClient,
  marketplace: Address
): Promise<ActiveListing[]> {
  const fromBlock = indexFromBlockEnv();
  const chunk = indexChunkBlocksEnv();
  const logs = await chunkForwardLogs(
    client,
    {address: marketplace, event: listedEvent},
    fromBlock,
    chunk
  );
  const pairs = uniqueListedPairs(logs);
  if (pairs.length === 0) return [];

  const now = BigInt(Math.floor(Date.now() / 1000));
  const out: ActiveListing[] = [];
  const BATCH = 150;

  for (let i = 0; i < pairs.length; i += BATCH) {
    const slice = pairs.slice(i, i + BATCH);
    const contracts = slice.map(({coll, id}) => ({
      address: marketplace,
      abi: MarketplaceAbi,
      functionName: "listings" as const,
      args: [coll, id] as const
    }));
    const results = await client.multicall({contracts, allowFailure: true});
    for (let j = 0; j < results.length; j++) {
      const r = results[j];
      if (r.status !== "success") continue;
      const row = r.result as readonly [Address, bigint, number, bigint, bigint];
      const [seller, expiresAt, standard, price] = row;
      if (seller === zeroAddress) continue;
      if (now > expiresAt) continue;
      if (Number(standard) !== 0) continue;
      out.push({
        coll: slice[j].coll,
        id: slice[j].id,
        price,
        expiresAt,
        seller
      });
    }
  }
  return out;
}

export async function fetchCollectionMeta(
  client: PublicClient,
  addresses: Address[]
): Promise<Record<string, CollectionMeta>> {
  const uniq = [...new Set(addresses.map(a => a.toLowerCase()))] as string[];
  const addrs = uniq.map(a => a as Address);
  const meta: Record<string, CollectionMeta> = {};
  const BATCH = 80;
  for (let i = 0; i < addrs.length; i += BATCH) {
    const slice = addrs.slice(i, i + BATCH);
    const nameCalls = slice.map(address => ({
      address,
      abi: ERC721Abi,
      functionName: "name" as const,
      args: [] as const
    }));
    const symCalls = slice.map(address => ({
      address,
      abi: ERC721Abi,
      functionName: "symbol" as const,
      args: [] as const
    }));
    const [names, syms] = await Promise.all([
      client.multicall({contracts: nameCalls, allowFailure: true}),
      client.multicall({contracts: symCalls, allowFailure: true})
    ]);
    for (let j = 0; j < slice.length; j++) {
      const key = slice[j].toLowerCase();
      const n = names[j].status === "success" ? String(names[j].result) : "Collection";
      const s = syms[j].status === "success" ? String(syms[j].result) : "";
      meta[key] = {name: n, symbol: s};
    }
  }
  return meta;
}

export function shuffleInPlace<T>(arr: T[]) {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
}
