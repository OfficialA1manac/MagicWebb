import {type Address, type PublicClient} from "viem";
import {ERC721Abi} from "@/lib/abi/ERC721";

/** OpenZeppelin IERC721Enumerable */
const IERC721_ENUMERABLE = "0x780e9d63" as `0x${string}`;

export type WalletToken = {coll: Address; id: bigint};

/**
 * Discover ERC-721 tokens `owner` holds in `collections`.
 * Enumerable collections use `tokenOfOwnerByIndex`; otherwise we scan low token IDs (cap) with `ownerOf`.
 */
export async function fetchWalletErc721Holdings(
  client: PublicClient,
  owner: Address,
  collections: Address[],
  scanCap = 512
): Promise<WalletToken[]> {
  const seenAddr = new Set<string>();
  const uniq: Address[] = [];
  for (const c of collections) {
    const lc = c.toLowerCase();
    if (seenAddr.has(lc)) continue;
    seenAddr.add(lc);
    uniq.push(c);
  }
  const out: WalletToken[] = [];
  const seen = new Set<string>();
  const add = (coll: Address, id: bigint) => {
    const k = `${coll.toLowerCase()}:${id.toString()}`;
    if (seen.has(k)) return;
    seen.add(k);
    out.push({coll, id});
  };

  for (const coll of uniq) {
    try {
      const bal = await client.readContract({
        address: coll,
        abi: ERC721Abi,
        functionName: "balanceOf",
        args: [owner]
      });
      if (bal === 0n) continue;

      let enumerable = false;
      try {
        enumerable = await client.readContract({
          address: coll,
          abi: ERC721Abi,
          functionName: "supportsInterface",
          args: [IERC721_ENUMERABLE]
        });
      } catch {
        enumerable = false;
      }

      if (enumerable) {
        const calls = [];
        for (let i = 0n; i < bal; i++) {
          calls.push({
            address: coll,
            abi: ERC721Abi,
            functionName: "tokenOfOwnerByIndex" as const,
            args: [owner, i] as const
          });
        }
        const res = await client.multicall({contracts: calls, allowFailure: true});
        for (const r of res) {
          if (r.status === "success") add(coll, r.result as bigint);
        }
      } else {
        let total: bigint | undefined;
        try {
          total = await client.readContract({
            address: coll,
            abi: ERC721Abi,
            functionName: "totalSupply",
            args: []
          });
        } catch {
          total = undefined;
        }
        const cap = total !== undefined ? Math.min(Number(total), scanCap) : scanCap;
        const ownerLc = owner.toLowerCase();
        const BATCH = 60;
        for (let start = 1; start <= cap; start += BATCH) {
          const calls = [];
          for (let tid = start; tid < start + BATCH && tid <= cap; tid++) {
            calls.push({
              address: coll,
              abi: ERC721Abi,
              functionName: "ownerOf" as const,
              args: [BigInt(tid)] as const
            });
          }
          const res = await client.multicall({contracts: calls, allowFailure: true});
          res.forEach((r, idx) => {
            if (r.status !== "success") return;
            const o = (r.result as string).toLowerCase();
            if (o === ownerLc) add(coll, BigInt(start + idx));
          });
        }
      }
    } catch {
      /* not a compatible ERC-721 */
    }
  }
  return out;
}
