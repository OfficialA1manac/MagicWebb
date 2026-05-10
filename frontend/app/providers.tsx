"use client";
import {WagmiProvider} from "wagmi";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {RainbowKitProvider, darkTheme} from "@rainbow-me/rainbowkit";
import {wagmiConfig} from "@/lib/wagmi";
import {useState, type ReactNode} from "react";

export function Providers({children}: {children: ReactNode}) {
  const [qc] = useState(() => new QueryClient());
  return (
    <WagmiProvider config={wagmiConfig}>
      <QueryClientProvider client={qc}>
        <RainbowKitProvider theme={darkTheme()}>{children}</RainbowKitProvider>
      </QueryClientProvider>
    </WagmiProvider>
  );
}
