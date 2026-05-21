"use client";
import {useQuery} from "@tanstack/react-query";
import {api} from "@/lib/api";

export function useServerTime() {
  const {data} = useQuery({
    queryKey: ["server-time"],
    queryFn: async () => {
      const {unix_ms} = await api.getServerTime();
      return {serverNow: Math.floor(unix_ms / 1000), fetchedAt: Date.now()};
    },
    staleTime: Infinity,
  });

  // Interpolate server time using elapsed local clock ticks.
  const nowSeconds = (): bigint => {
    if (!data) return BigInt(Math.floor(Date.now() / 1000));
    const elapsed = Math.floor((Date.now() - data.fetchedAt) / 1000);
    return BigInt(data.serverNow + elapsed);
  };

  return {nowSeconds};
}
