import {type Address, type PublicClient} from "viem";
import {ERC721Abi} from "@/lib/abi/ERC721";

const IERC721_ENUMERABLE = "0x780e9d63" as `0x${string}`;

export type WalletToken = {coll: Address; id: bigint};

async function scanCollection(
  client: PublicClient,
  coll: Address,
  owner: Address,
  scanCap: number
): Promise<WalletToken[]> {
  const out: WalletToken[] = [];
  const seen = new Set<string>();
  const add = (id: bigint) => {
    const k = id.toString();
    if (!seen.has(k)) { seen.add(k); out.push({coll, id}); }
  };

  try {
    const bal = await client.readContract({
      address: coll, abi: ERC721Abi, functionName: "balanceOf", args: [owner]
    });
    if (bal === 0n) return out;

    let enumerable = false;
    try {
      enumerable = await client.readContract({
        address: coll, abi: ERC721Abi,
        functionName: "supportsInterface", args: [IERC721_ENUMERABLE]
      });
    } catch { enumerable = false; }

    if (enumerable) {
      const calls = Array.from({length: Number(bal)}, (_, i) => ({
        address: coll, abi: ERC721Abi,
        functionName: "tokenOfOwnerByIndex" as const,
        args: [owner, BigInt(i)] as const,
      }));
      const res = await client.multicall({contracts: calls, allowFailure: true});
      for (const r of res) {
        if (r.status === "success") add(r.result as bigint);
      }
    } else {
      let total: bigint | undefined;
      try {
        total = await client.readContract({
          address: coll, abi: ERC721Abi, functionName: "totalSupply", args: []
        });
      } catch { total = undefined; }

      const cap = total !== undefined ? Math.min(Number(total), scanCap) : scanCap;
      const ownerLc = owner.toLowerCase();
      const BATCH = 60;
      for (let start = 0; start <= cap; start += BATCH) {
        const calls = [];
        for (let tid = start; tid < start + BATCH && tid <= cap; tid++) {
          calls.push({
            address: coll, abi: ERC721Abi,
            functionName: "ownerOf" as const,
            args: [BigInt(tid)] as const,
          });
        }
        const res = await client.multicall({contracts: calls, allowFailure: true});
        res.forEach((r, idx) => {
          if (r.status !== "success") return;
          if ((r.result as string).toLowerCase() === ownerLc) add(BigInt(start + idx));
        });
      }
    }
  } catch { /* non-ERC721 contract — skip */ }

  return out;
}

/**
 * Discover ERC-721 tokens `owner` holds across `collections`.
 * All collections scanned in parallel (Promise.all) — fixes H4 audit finding.
 */
export async function fetchWalletErc721Holdings(
  client: PublicClient,
  owner: Address,
  collections: Address[],
  scanCap = 512
): Promise<WalletToken[]> {
  const seen = new Set<string>();
  const uniq: Address[] = [];
  for (const c of collections) {
    const lc = c.toLowerCase();
    if (!seen.has(lc)) { seen.add(lc); uniq.push(c); }
  }

  const results = await Promise.all(
    uniq.map(coll => scanCollection(client, coll, owner, scanCap))
  );

  const globalSeen = new Set<string>();
  const out: WalletToken[] = [];
  for (const tokens of results) {
    for (const t of tokens) {
      const k = `${t.coll.toLowerCase()}:${t.id}`;
      if (!globalSeen.has(k)) { globalSeen.add(k); out.push(t); }
    }
  }
  return out;
}
