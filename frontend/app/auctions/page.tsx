"use client";
import {useState} from "react";
import Link from "next/link";
import {formatEther, type Address} from "viem";
import {useChainAuctions, type AuctionRow} from "@/hooks/useChainAuctions";

function AuctionCard({row, meta}: {row: AuctionRow; meta?: {name: string; symbol: string}}) {
  const now = BigInt(Math.floor(Date.now() / 1000));
  const live = !row.settled && now >= row.startsAt && now < row.endsAt;
  const upcoming = !row.settled && now < row.startsAt;
  const endedAwaiting = !row.settled && now >= row.endsAt;

  const label = row.settled
    ? "Settled / closed"
    : live
      ? "Live"
      : upcoming
        ? "Upcoming"
        : endedAwaiting
          ? "Ended (settle / no bids)"
          : "—";

  const labelCls = row.settled
    ? "text-neutral-500"
    : live
      ? "text-emerald-400"
      : upcoming
        ? "text-sky-400"
        : "text-amber-400";

  const collName = meta?.name ?? "Collection";
  const sym = meta?.symbol ? ` · ${meta.symbol}` : "";

  return (
    <Link
      href={`/auction/${row.id.toString()}`}
      className="block rounded-xl border border-neutral-800 bg-neutral-900/40 p-4 transition hover:border-neutral-600 hover:bg-neutral-900/60"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="text-xs font-mono text-neutral-500">Auction #{row.id.toString()}</div>
          <div className="mt-1 truncate text-sm font-semibold text-neutral-100">
            {collName}
            <span className="font-normal text-neutral-500">{sym}</span>
          </div>
          <div className="mt-1 break-all font-mono text-xs text-neutral-500">{row.collection}</div>
          <div className="mt-2 text-lg font-mono text-neutral-200">Token #{row.tokenId.toString()}</div>
        </div>
        <div className={`shrink-0 text-right text-xs font-medium ${labelCls}`}>{label}</div>
      </div>
      <div className="mt-3 flex flex-wrap items-baseline justify-between gap-2 border-t border-neutral-800/80 pt-3 text-sm">
        <span className="text-neutral-500">High bid</span>
        <span className="font-mono text-emerald-400">{formatEther(row.highestBid)} C2FLR</span>
      </div>
      <div className="mt-1 text-xs text-neutral-500">
        Reserve {formatEther(row.reserve)} · Ends {new Date(Number(row.endsAt) * 1000).toLocaleString()}
      </div>
    </Link>
  );
}

function AuctionGrid({rows, meta}: {rows: AuctionRow[]; meta: Record<string, {name: string; symbol: string}>}) {
  if (rows.length === 0) {
    return <p className="text-sm text-neutral-500">Nothing in this tab yet.</p>;
  }
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {rows.map(row => (
        <AuctionCard key={row.id.toString()} row={row} meta={meta[row.collection.toLowerCase()]} />
      ))}
    </div>
  );
}

export default function AuctionsPage() {
  const {data, isLoading, error, refetch} = useChainAuctions();
  const [tab, setTab] = useState<"open" | "closed">("open");

  const meta = data?.meta ?? {};

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl md:text-3xl font-bold">Auctions</h1>
          <p className="mt-2 max-w-2xl text-sm text-neutral-400">
            Open = not settled and before <span className="font-mono text-neutral-300">endsAt</span>. Closed = settled (sold /
            cancelled) or time expired. Data is read directly from <span className="font-mono">AuctionHouse</span>.
          </p>
        </div>
        <button
          type="button"
          className="self-start rounded-lg border border-neutral-600 px-4 py-2 text-sm hover:border-emerald-500/50"
          onClick={() => refetch()}
        >
          Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-lg border border-red-900/50 bg-red-950/30 px-4 py-3 text-sm text-red-200">
          {(error as Error).message}
        </div>
      )}

      {isLoading && <div className="text-sm text-neutral-500">Loading auctions…</div>}

      {!isLoading && data && (
        <>
          <div className="flex flex-wrap gap-2 border-b border-neutral-800 pb-1">
            <button
              type="button"
              className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
                tab === "open" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
              }`}
              onClick={() => setTab("open")}
            >
              Open ({data.open.length})
            </button>
            <button
              type="button"
              className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
                tab === "closed" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
              }`}
              onClick={() => setTab("closed")}
            >
              Closed ({data.closed.length})
            </button>
          </div>

          {tab === "open" ? (
            <AuctionGrid rows={data.open} meta={meta} />
          ) : (
            <AuctionGrid rows={data.closed} meta={meta} />
          )}
        </>
      )}
    </div>
  );
}
