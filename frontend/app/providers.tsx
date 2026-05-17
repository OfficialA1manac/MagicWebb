"use client";
import {WagmiProvider} from "wagmi";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {wagmiConfig} from "@/lib/wagmi";
import {FavoritesProvider} from "@/context/FavoritesContext";
import {useState, type ReactNode} from "react";

export function Providers({children}: {children: ReactNode}) {
  const [qc] = useState(() => new QueryClient({
    defaultOptions: {
      queries: {staleTime: 10_000, refetchOnWindowFocus: false, retry: 2}
    }
  }));

  return (
    <WagmiProvider config={wagmiConfig} reconnectOnMount={false}>
      <QueryClientProvider client={qc}>
        <FavoritesProvider>{children}</FavoritesProvider>
      </QueryClientProvider>
    </WagmiProvider>
  );
}
