"use client";
import {Suspense, useEffect, useState} from "react";
import {useSearchParams} from "next/navigation";
import Link from "next/link";
import {useQuery} from "@tanstack/react-query";
import {api, type SearchItem} from "@/lib/api";

function ResultCard({item}: {item: SearchItem}) {
  if (item.kind === "collection") {
    return (
      <Link
        href={`/collection/${item.collection}`}
        className="block rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 transition hover:border-neutral-600 hover:bg-neutral-900/60"
      >
        <div className="text-xs font-medium uppercase tracking-wide text-emerald-400/80">Collection</div>
        <div className="mt-1 truncate font-semibold text-neutral-100">{item.name || "Unnamed"}</div>
        <div className="mt-1 truncate font-mono text-xs text-neutral-500">{item.collection}</div>
      </Link>
    );
  }
  return (
    <Link
      href={`/token/${item.collection}/${item.token_id}`}
      className="block rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 transition hover:border-neutral-600 hover:bg-neutral-900/60"
    >
      {item.image_uri && (
        <img
          src={item.image_uri}
          alt={item.name}
          className="mb-3 h-32 w-full rounded-lg object-cover"
        />
      )}
      <div className="text-xs font-medium uppercase tracking-wide text-neutral-500">NFT</div>
      <div className="mt-1 truncate font-semibold text-neutral-100">
        {item.name || `#${item.token_id}`}
      </div>
      <div className="mt-1 truncate font-mono text-xs text-neutral-500">{item.collection}</div>
    </Link>
  );
}

function SearchResults({q}: {q: string}) {
  const {data, isLoading, error} = useQuery<SearchItem[]>({
    queryKey: ["search", q],
    queryFn: () => api.search(q),
    enabled: q.length >= 2,
  });

  if (q.length < 2) {
    return <p className="text-sm text-neutral-500">Type at least 2 characters to search.</p>;
  }
  if (isLoading) return <p className="text-sm text-neutral-500">Searching…</p>;
  if (error) return <p className="text-sm text-red-400">{(error as Error).message}</p>;
  if (!data?.length) return <p className="text-sm text-neutral-500">No results for &ldquo;{q}&rdquo;.</p>;

  const collections = data.filter(i => i.kind === "collection");
  const nfts = data.filter(i => i.kind === "nft");

  return (
    <div className="space-y-8">
      {collections.length > 0 && (
        <section>
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-neutral-400">
            Collections ({collections.length})
          </h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {collections.map(item => (
              <ResultCard key={item.collection} item={item} />
            ))}
          </div>
        </section>
      )}
      {nfts.length > 0 && (
        <section>
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-neutral-400">
            NFTs ({nfts.length})
          </h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {nfts.map(item => (
              <ResultCard key={`${item.collection}/${item.token_id}`} item={item} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function SearchPage() {
  const params = useSearchParams();
  const initial = params.get("q") ?? "";
  const [input, setInput] = useState(initial);
  const [q, setQ] = useState(initial);

  useEffect(() => {
    const id = setTimeout(() => {
      const trimmed = input.trim();
      setQ(trimmed);
      const url = trimmed ? `/search?q=${encodeURIComponent(trimmed)}` : "/search";
      window.history.replaceState(null, "", url);
    }, 300);
    return () => clearTimeout(id);
  }, [input]);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">Search</h1>
        <p className="mt-1 text-sm text-neutral-400">Find NFTs and collections by name or description.</p>
      </div>
      <input
        autoFocus
        type="search"
        placeholder="Search NFTs, collections…"
        value={input}
        onChange={e => setInput(e.target.value)}
        className="w-full rounded-xl border border-neutral-700 bg-neutral-900 px-4 py-3 text-sm placeholder-neutral-500 focus:border-emerald-500/50 focus:outline-none"
      />
      <SearchResults q={q} />
    </div>
  );
}

export default function Page() {
  return (
    <Suspense>
      <SearchPage />
    </Suspense>
  );
}
