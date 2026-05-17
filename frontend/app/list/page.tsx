"use client";
import {useMemo, useState} from "react";
import {useAccount} from "wagmi";
import {type Address} from "viem";
import {useFavorites} from "@/context/FavoritesContext";
import {useWalletHoldings} from "@/hooks/useWalletHoldings";
import {useChainListings} from "@/hooks/useChainListings";
import {ProfileNftCard} from "@/components/ProfileNftCard";
import type {ActiveListing} from "@/lib/marketIndex";

export default function ListNftPage() {
  const {address, isConnected} = useAccount();
  const {favoritesKey} = useFavorites();

  const {
    data: walletPack,
    isPending: walletPending,
    error: walletErr,
    refetch: refetchWallet,
  } = useWalletHoldings(address, favoritesKey);

  const {data: marketData, refetch: refetchListings} = useChainListings();

  const listingLookup = useMemo(() => {
    const m = new Map<string, ActiveListing>();
    if (!marketData?.listings) return m;
    for (const l of marketData.listings) {
      m.set(`${l.coll.toLowerCase()}:${l.id.toString()}`, l);
    }
    return m;
  }, [marketData?.listings]);

  const [hiddenKeys, setHiddenKeys] = useState<Set<string>>(() => {
    if (typeof window === "undefined") return new Set();
    try {
      const s = localStorage.getItem("mw:hidden-tokens");
      return new Set(s ? (JSON.parse(s) as string[]) : []);
    } catch { return new Set(); }
  });

  const toggleHide = (coll: Address, id: bigint) => {
    setHiddenKeys(prev => {
      const k = `${coll.toLowerCase()}:${id.toString()}`;
      const next = new Set(prev);
      if (next.has(k)) next.delete(k); else next.add(k);
      try { localStorage.setItem("mw:hidden-tokens", JSON.stringify([...next])); } catch {}
      return next;
    });
  };

  const refreshAfterAction = () => {
    void refetchWallet();
    void refetchListings();
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">List an NFT</h1>
        <p className="mt-2 max-w-3xl text-sm text-neutral-400">
          Your ERC-721 tokens are shown below. Click <strong className="text-neutral-300">List</strong> to set a fixed
          price and duration, or <strong className="text-neutral-300">Unlist</strong> to cancel an active listing. For
          auctions, open the token detail page.
        </p>
      </div>

      {!isConnected && (
        <div className="rounded-lg border border-amber-900/50 bg-amber-950/30 px-4 py-3 text-sm text-amber-200/90">
          Connect your wallet to see tokens you own.
        </div>
      )}

      {isConnected && walletPending && (
        <div className="flex items-center gap-2 text-sm text-neutral-500">
          <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-neutral-600 border-t-emerald-400" />
          Loading tokens from indexed collections…
        </div>
      )}

      {isConnected && walletErr && (
        <p className="text-sm text-red-400">{(walletErr as Error).message}</p>
      )}

      {isConnected && !walletPending && walletPack && walletPack.tokens.length === 0 && (
        <div className="rounded-xl border border-dashed border-neutral-700 p-8 text-center text-sm text-neutral-500">
          No ERC-721 tokens found in indexed collections. Make sure{" "}
          <span className="font-mono text-neutral-400">NEXT_PUBLIC_TRACKED_COLLECTIONS</span> includes your
          collection address, then refresh.
        </div>
      )}

      {isConnected && walletPack && walletPack.tokens.length > 0 && (
        <div className="grid gap-4 grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
          {walletPack.tokens.map(t => {
            const k = `${t.coll.toLowerCase()}:${t.id.toString()}`;
            const m = walletPack.meta[t.coll.toLowerCase()];
            const listing = listingLookup.get(k);
            return (
              <ProfileNftCard
                key={k}
                coll={t.coll}
                id={t.id}
                collectionName={m?.name}
                listing={listing}
                hidden={hiddenKeys.has(k)}
                onToggleHide={() => toggleHide(t.coll, t.id)}
                onActionDone={refreshAfterAction}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
