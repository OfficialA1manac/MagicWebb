"use client";
import {useParams, useRouter} from "next/navigation";
import {useState} from "react";
import type {Address} from "viem";
import {useReadContract} from "wagmi";
import {ERC721Abi} from "@/lib/abi";

export default function Collection() {
  const {addr} = useParams<{addr: string}>();
  const coll = addr as Address;
  const router = useRouter();
  const [tid, setTid] = useState("1");

  const {data: name, isLoading: nameLoading}   = useReadContract({address: coll, abi: ERC721Abi, functionName: "name"});
  const {data: symbol} = useReadContract({address: coll, abi: ERC721Abi, functionName: "symbol"});

  const go = () => {
    const n = parseInt(tid, 10);
    if (!tid || isNaN(n) || n <= 0) return;
    router.push(`/token/${coll}/${n}`);
  };

  return (
    <div className="space-y-4 max-w-xl">
      <div>
        <div className="text-xs text-neutral-400 break-all">{coll}</div>
        <h1 className="text-2xl md:text-3xl font-bold">
          {nameLoading ? "…" : (name as string) ?? "Collection"}{" "}
          <span className="text-neutral-500 text-base">{(symbol as string) ?? ""}</span>
        </h1>
      </div>
      <p className="text-sm text-neutral-400">
        Enter a token ID to view its on-chain listing / auction / offer state.
      </p>
      <div className="flex flex-col sm:flex-row gap-2">
        <input
          className="flex-1 bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
          placeholder="Token ID (e.g. 1)"
          value={tid} onChange={e => setTid(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") go(); }}
        />
        <button
          className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
          disabled={!tid} onClick={go}
        >View token</button>
      </div>
    </div>
  );
}
