"use client";
import {useWriteContract, useAccount} from "wagmi";
import {encodeAbiParameters, keccak256, parseAbiParameters} from "viem";
import {ADDR} from "@/lib/addresses";
import {AuctionHouseAbi} from "@/lib/abi";

export type PendingCommit = {
  auctionId: string;
  bidder: string;
  fullAmount: string;
  salt: `0x${string}`;
  commitBlock: number;
};

const COMMIT_KEY = "mw:bid-commits";

export function storePendingCommit(c: PendingCommit) {
  try {
    const all = getAllPendingCommits();
    all[c.auctionId] = c;
    localStorage.setItem(COMMIT_KEY, JSON.stringify(all));
  } catch {}
}

export function getAllPendingCommits(): Record<string, PendingCommit> {
  try {
    const s = localStorage.getItem(COMMIT_KEY);
    return s ? (JSON.parse(s) as Record<string, PendingCommit>) : {};
  } catch { return {}; }
}

export function getPendingCommit(auctionId: bigint): PendingCommit | null {
  const all = getAllPendingCommits();
  return all[auctionId.toString()] ?? null;
}

export function clearPendingCommit(auctionId: bigint) {
  try {
    const all = getAllPendingCommits();
    delete all[auctionId.toString()];
    localStorage.setItem(COMMIT_KEY, JSON.stringify(all));
  } catch {}
}

export function buildCommitment(
  id: bigint,
  bidder: `0x${string}`,
  fullBidAmount: bigint,
  salt: `0x${string}`
): `0x${string}` {
  return keccak256(
    encodeAbiParameters(
      parseAbiParameters("uint256, address, uint128, bytes32"),
      [id, bidder, fullBidAmount, salt]
    )
  );
}

export function useCommitBid() {
  const {writeContractAsync, isPending, error} = useWriteContract();
  const {address} = useAccount();

  const commitBid = async (id: bigint, fullBidAmount: bigint) => {
    if (!address) throw new Error("wallet not connected");
    const saltBytes = crypto.getRandomValues(new Uint8Array(32));
    const salt = `0x${Array.from(saltBytes).map(b => b.toString(16).padStart(2, "0")).join("")}` as `0x${string}`;
    const commitment = buildCommitment(id, address, fullBidAmount, salt);
    const hash = await writeContractAsync({
      address: ADDR.auction,
      abi: AuctionHouseAbi,
      functionName: "commitBid",
      args: [id, commitment],
    });
    storePendingCommit({
      auctionId: id.toString(),
      bidder: address,
      fullAmount: fullBidAmount.toString(),
      salt,
      commitBlock: 0,
    });
    return {hash, salt, fullBidAmount};
  };

  return {commitBid, isPending, error};
}
