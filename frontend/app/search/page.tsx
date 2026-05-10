"use client";
import {useState} from "react";
import {useRouter} from "next/navigation";
import {isAddress} from "viem";

export default function Search() {
  const [q, setQ] = useState("");
  const router = useRouter();
  const go = () => {
    if (isAddress(q)) router.push(`/collection/${q}`);
  };
  return (
    <div className="max-w-xl mx-auto space-y-4">
      <h1 className="text-2xl font-bold">Search</h1>
      <p className="text-sm text-neutral-400">Enter a collection or wallet address.</p>
      <div className="flex gap-2">
        <input
          className="flex-1 bg-neutral-900 border border-neutral-700 rounded px-3 py-2"
          placeholder="0x..."
          value={q} onChange={e => setQ(e.target.value)}
          onKeyDown={e => {if (e.key === "Enter") go();}}
        />
        <button
          className="px-4 py-2 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
          disabled={!isAddress(q)} onClick={go}
        >Go</button>
      </div>
    </div>
  );
}
