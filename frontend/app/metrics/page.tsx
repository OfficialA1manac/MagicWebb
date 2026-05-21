"use client";
import {useQuery} from "@tanstack/react-query";
import {useSubscription} from "urql";
import {useState} from "react";
import {api} from "@/lib/api";

type Metrics = {
  totalActiveListings: number;
  totalSales: number;
  grossVolumeWei: string;
  totalAuctions: number;
};

type ActivityRow = {
  type: string;
  collection: string;
  tokenId: string;
  amountWei: string;
  txHash: string;
  timestamp: number;
};

const ACTIVITY_SUB = `subscription {
  activityFeed {
    type collection tokenId amountWei txHash timestamp
  }
}`;

function shortAddr(s: string) {
  if (!s || s.length < 10) return s;
  return `${s.slice(0, 6)}…${s.slice(-4)}`;
}

function formatWei(wei: string | undefined) {
  if (!wei) return "—";
  try {
    const n = Number(BigInt(wei)) / 1e18;
    return n.toFixed(4);
  } catch { return "—"; }
}

export default function MetricsPage() {
  const {data: metrics} = useQuery<Metrics>({
    queryKey: ["market-metrics"],
    queryFn: () => api.getMetrics() as Promise<Metrics>,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });

  const {data: initActivity = []} = useQuery<ActivityRow[]>({
    queryKey: ["activity-init"],
    queryFn: () => api.getActivity(50) as Promise<ActivityRow[]>,
    staleTime: 30_000,
  });

  const [liveItems, setLiveItems] = useState<ActivityRow[]>([]);

  useSubscription({query: ACTIVITY_SUB}, (_, data: {activityFeed: ActivityRow}) => {
    const item = data?.activityFeed;
    if (!item) return data;
    setLiveItems(prev => [item, ...prev].slice(0, 50));
    return data;
  });

  const feed = [...liveItems, ...initActivity].slice(0, 50);

  const statCards = [
    {label: "Active Listings", value: metrics?.totalActiveListings ?? "—"},
    {label: "Gross Volume (FLR)", value: metrics ? formatWei(metrics.grossVolumeWei) : "—"},
    {label: "Total Sales", value: metrics?.totalSales ?? "—"},
    {label: "Total Auctions", value: metrics?.totalAuctions ?? "—"},
  ];

  return (
    <div className="space-y-8">
      <h1 className="text-2xl font-bold md:text-3xl">Market Metrics</h1>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {statCards.map(c => (
          <div key={c.label} className="rounded-xl border border-neutral-800 bg-neutral-900/40 p-4">
            <div className="text-xs text-neutral-500">{c.label}</div>
            <div className="mt-1 text-2xl font-bold text-neutral-100">{String(c.value)}</div>
          </div>
        ))}
      </div>

      <div>
        <h2 className="text-lg font-semibold mb-3">Live Activity</h2>
        {feed.length === 0 ? (
          <p className="text-sm text-neutral-500">No activity yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs text-left">
              <thead>
                <tr className="border-b border-neutral-800 text-neutral-500">
                  <th className="py-2 pr-4">Type</th>
                  <th className="py-2 pr-4">Collection / Token</th>
                  <th className="py-2 pr-4">Amount (FLR)</th>
                  <th className="py-2 pr-4">Tx</th>
                  <th className="py-2">Time</th>
                </tr>
              </thead>
              <tbody>
                {feed.map((row, i) => (
                  <tr key={`${row.txHash}-${i}`} className="border-b border-neutral-800/50 hover:bg-neutral-800/20">
                    <td className="py-2 pr-4 font-medium text-emerald-400">{row.type}</td>
                    <td className="py-2 pr-4 font-mono">
                      {shortAddr(row.collection)}
                      {row.tokenId ? <span className="text-neutral-500"> #{row.tokenId}</span> : null}
                    </td>
                    <td className="py-2 pr-4">{formatWei(row.amountWei)}</td>
                    <td className="py-2 pr-4 font-mono">
                      {row.txHash ? (
                        <a
                          href={`https://coston2-explorer.flare.network/tx/${row.txHash}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-emerald-400 hover:underline"
                        >
                          {row.txHash.slice(0, 10)}…
                        </a>
                      ) : "—"}
                    </td>
                    <td className="py-2 text-neutral-500">
                      {row.timestamp ? new Date(row.timestamp * 1000).toLocaleTimeString() : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
