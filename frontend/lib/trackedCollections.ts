import {isAddress, type Address} from "viem";

/** Comma-separated ERC-721 collection addresses to always index for wallet holdings (optional). */
export function readTrackedCollectionAddresses(): Address[] {
  const raw = process.env.NEXT_PUBLIC_TRACKED_COLLECTIONS?.trim();
  if (!raw) return [];
  return raw
    .split(",")
    .map(s => s.trim())
    .filter((s): s is Address => isAddress(s));
}

/** First block to scan for `Listed` logs (default 0 = full history; set on busy chains to speed up). */
export function indexFromBlockEnv(): bigint {
  const raw = process.env.NEXT_PUBLIC_INDEX_FROM_BLOCK?.trim();
  if (!raw) return 0n;
  try {
    return BigInt(raw);
  } catch {
    return 0n;
  }
}

export function indexChunkBlocksEnv(): bigint {
  const raw = process.env.NEXT_PUBLIC_INDEX_CHUNK_BLOCKS?.trim();
  if (!raw) return 25_000n;
  try {
    const n = BigInt(raw);
    return n > 0n ? n : 25_000n;
  } catch {
    return 25_000n;
  }
}
