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

/** Blocks per `eth_getLogs` range (inclusive span). Default 30 — Flare Coston2 public RPC caps log scans (~30 blocks). */
export function indexChunkBlocksEnv(): bigint {
  const raw = process.env.NEXT_PUBLIC_INDEX_CHUNK_BLOCKS?.trim();
  if (!raw) return 30n;
  try {
    const n = BigInt(raw);
    return n > 0n ? n : 30n;
  } catch {
    return 30n;
  }
}

/**
 * Hard cap per `getLogs` request (inclusive `fromBlock`…`toBlock` range size).
 * Coston2 `https://coston2-api.flare.network/ext/C/rpc` rejects large spans (e.g. 25_000).
 * Set `NEXT_PUBLIC_INDEX_GETLOGS_BLOCK_CAP=0` to disable capping (private RPCs only).
 */
export function indexGetlogsBlockCapEnv(): bigint | null {
  const raw = process.env.NEXT_PUBLIC_INDEX_GETLOGS_BLOCK_CAP?.trim();
  if (raw === "0") return null;
  if (!raw) return 30n;
  try {
    const n = BigInt(raw);
    return n > 0n ? n : 30n;
  } catch {
    return 30n;
  }
}
