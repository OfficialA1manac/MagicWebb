"use client";
import {useMemo, useState} from "react";
import Link from "next/link";
import {isAddress, type Address} from "viem";
import {useAccount, useReadContract} from "wagmi";
import {ERC721Abi, MarketplaceAbi} from "@/lib/abi";
import {ADDR} from "@/lib/addresses";
import {OwnerActions} from "@/components/OwnerActions";

export default function ListNftPage() {
  const {address, isConnected} = useAccount();
  const [rawColl, setRawColl] = useState("");
  const [rawId, setRawId] = useState("");

  const coll = useMemo(() => {
    const t = rawColl.trim();
    return isAddress(t) ? (t as Address) : null;
  }, [rawColl]);

  const tokenId = useMemo(() => {
    try {
      if (!rawId.trim()) return null;
      return BigInt(rawId.trim());
    } catch {
      return null;
    }
  }, [rawId]);

  const {data: owner, isLoading: ownerLoading} = useReadContract({
    address: coll ?? undefined,
    abi: ERC721Abi,
    functionName: "ownerOf",
    args: coll && tokenId !== null ? [tokenId] : undefined,
    query: {enabled: !!coll && tokenId !== null}
  });

  const {data: listing} = useReadContract({
    address: ADDR.marketplace,
    abi: MarketplaceAbi,
    functionName: "listings",
    args: coll && tokenId !== null ? [coll, tokenId] : undefined,
    query: {enabled: !!coll && tokenId !== null}
  });

  const [seller] = (listing as [Address, bigint, bigint] | undefined) ??
    ["0x0000000000000000000000000000000000000000" as Address, 0n, 0n];
  const isListed = seller !== "0x0000000000000000000000000000000000000000";
  const isOwner =
    !!address &&
    !!owner &&
    typeof owner === "string" &&
    address.toLowerCase() === (owner as string).toLowerCase();

  const validInput = !!coll && tokenId !== null;

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">List or auction an NFT</h1>
        <p className="mt-2 text-sm text-neutral-400">
          Enter your ERC-721 collection and token ID. You must be the on-chain owner. Choose a fixed-price listing or an
          English auction — both settle on Flare Coston2 through the same contracts as the rest of MagicWebb.
        </p>
      </div>

      <section className="space-y-4 rounded-xl border border-neutral-800 bg-neutral-900/40 p-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block text-sm">
            <span className="text-neutral-400">Collection (ERC-721)</span>
            <input
              className="mt-1 w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 font-mono text-sm"
              placeholder="0x…"
              value={rawColl}
              onChange={e => setRawColl(e.target.value)}
              spellCheck={false}
            />
          </label>
          <label className="block text-sm">
            <span className="text-neutral-400">Token ID</span>
            <input
              className="mt-1 w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 font-mono text-sm"
              placeholder="e.g. 42"
              value={rawId}
              onChange={e => setRawId(e.target.value)}
            />
          </label>
        </div>

        {!isConnected && (
          <p className="rounded-md border border-amber-900/50 bg-amber-950/30 px-3 py-2 text-sm text-amber-200/90">
            Connect your wallet (header) — it must be the wallet that owns the NFT.
          </p>
        )}

        {rawColl.trim() && !coll && (
          <p className="text-sm text-red-400">That collection address is not a valid checksummed 0x address.</p>
        )}
        {rawId.trim() && tokenId === null && (
          <p className="text-sm text-red-400">Token ID must be a non-negative whole number.</p>
        )}

        {validInput && coll && tokenId !== null && (
          <div className="space-y-3 text-sm">
            <div className="rounded-md border border-neutral-800 bg-neutral-950/60 px-3 py-2">
              <div className="text-neutral-500">Owner</div>
              <div className="break-all font-mono text-neutral-200">
                {ownerLoading ? "Loading…" : (owner as string) ?? "—"}
              </div>
            </div>
            {!ownerLoading && owner && !isOwner && (
              <p className="text-red-400">
                Connected wallet is not the owner of this token. Switch wallet or double-check collection and ID.
              </p>
            )}
            {isOwner && (
              <>
                <OwnerActions coll={coll} tokenId={tokenId} isListed={isListed} />
                <p className="text-xs text-neutral-500">
                  <strong className="text-neutral-400">Offers:</strong> buyers make EIP-712 offers from the{" "}
                  <Link href={`/token/${coll}/${tokenId.toString()}`} className="text-emerald-400 underline">
                    token page
                  </Link>{" "}
                  (you can share that link). Sellers redeem offers with{" "}
                  <Link href="/offer/accept" className="text-emerald-400 underline">
                    Accept offer
                  </Link>
                  .
                </p>
              </>
            )}
          </div>
        )}
      </section>

      <section className="rounded-xl border border-neutral-800 p-5 text-sm text-neutral-400">
        <h2 className="mb-2 font-semibold text-neutral-200">ERC-1155</h2>
        <p>
          Multi-edition listings use the same contracts with different contract functions. Use the token URL from search
          or your indexer for ERC-1155 flows, or extend the app with amount fields — ERC-721 is supported here first.
        </p>
      </section>
    </div>
  );
}
