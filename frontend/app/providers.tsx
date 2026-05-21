"use client";
import {WagmiProvider} from "wagmi";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {Provider as UrqlProvider} from "urql";
import {wagmiConfig} from "@/lib/wagmi";
import {urqlClient} from "@/lib/urql";
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
        <UrqlProvider value={urqlClient}>
          <FavoritesProvider>{children}</FavoritesProvider>
        </UrqlProvider>
      </QueryClientProvider>
    </WagmiProvider>
  );
}
