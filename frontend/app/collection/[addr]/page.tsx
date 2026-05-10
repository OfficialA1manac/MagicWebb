"use client";
import {useParams} from "next/navigation";
import Link from "next/link";
import type {Address} from "viem";
import {useReadContract} from "wagmi";
import {ERC721Abi} from "@/lib/abi";

export default function Collection() {
  const {addr} = useParams<{addr: string}>();
  const coll = addr as Address;
  const {data: name}   = useReadContract({address: coll, abi: ERC721Abi, functionName: "name"});
  const {data: symbol} = useReadContract({address: coll, abi: ERC721Abi, functionName: "symbol"});

  return (
    <div className="space-y-4">
      <div>
        <div className="text-xs text-neutral-400 break-all">{coll}</div>
        <h1 className="text-2xl font-bold">{(name as string) ?? "Collection"} <span className="text-neutral-500 text-base">{(symbol as string) ?? ""}</span></h1>
      </div>
      <p className="text-sm text-neutral-400">
        Look up a specific token to view its listing / auction / offer state.
      </p>
      <Link className="underline" href={`/token/${coll}/1`}>View token #1 →</Link>
    </div>
  );
}
