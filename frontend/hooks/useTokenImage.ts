"use client";
import {useQuery} from "@tanstack/react-query";
import {usePublicClient} from "wagmi";
import type {Address} from "viem";
import {ERC721Abi} from "@/lib/abi";

function resolveIpfs(uri: string): string {
  if (uri.startsWith("ipfs://")) return `https://ipfs.io/ipfs/${uri.slice(7)}`;
  return uri;
}

async function resolveTokenImage(tokenUri: string): Promise<string | null> {
  if (!tokenUri) return null;
  if (tokenUri.startsWith("data:application/json")) {
    try {
      const json = JSON.parse(atob(tokenUri.split(",")[1]));
      return json.image ? resolveIpfs(json.image as string) : null;
    } catch { return null; }
  }
  try {
    const res = await fetch(resolveIpfs(tokenUri), {signal: AbortSignal.timeout(6000)});
    if (!res.ok) return null;
    const meta = await res.json() as {image?: string};
    return meta.image ? resolveIpfs(meta.image) : null;
  } catch { return null; }
}

export function useTokenImage(coll: Address, id: bigint) {
  const client = usePublicClient();
  return useQuery({
    queryKey: ["token-image", coll, id.toString()],
    queryFn: async () => {
      if (!client) return null;
      try {
        const uri = await client.readContract({
          address: coll,
          abi: ERC721Abi,
          functionName: "tokenURI",
          args: [id]
        }) as string;
        return resolveTokenImage(uri);
      } catch { return null; }
    },
    enabled: !!client,
    staleTime: 5 * 60_000,
    retry: false
  });
}
