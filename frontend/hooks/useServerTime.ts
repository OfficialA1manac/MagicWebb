"use client";
import {useQuery} from "@tanstack/react-query";

export function useServerTime() {
  const {data} = useQuery({
    queryKey: ["server-time"],
    queryFn: async () => {
      const res = await fetch("/api/time");
      if (!res.ok) throw new Error("time fetch failed");
      const {now} = await res.json() as {now: number};
      return {serverNow: now, fetchedAt: Date.now()};
    },
    staleTime: 60_000,
    refetchInterval: 60_000,
  });

  // Interpolate server time using elapsed local clock ticks.
  const nowSeconds = (): bigint => {
    if (!data) return BigInt(Math.floor(Date.now() / 1000));
    const elapsed = Math.floor((Date.now() - data.fetchedAt) / 1000);
    return BigInt(data.serverNow + elapsed);
  };

  return {nowSeconds};
}
