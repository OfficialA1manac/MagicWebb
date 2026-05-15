"use client";
import Link from "next/link";
import {useAccount} from "wagmi";
import {useFavorites} from "@/context/FavoritesContext";
import {useWalletHoldings} from "@/hooks/useWalletHoldings";
import {ListableTokenCard} from "@/components/ListableTokenCard";

export default function ListNftPage() {
  const {address, isConnected} = useAccount();
  const {favoritesKey} = useFavorites();
  const {data: walletPack, isPending: walletPending, error: walletErr} = useWalletHoldings(
    address,
    favoritesKey
  );

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">List or auction an NFT</h1>
        <p className="mt-2 max-w-3xl text-sm text-neutral-400">
          Choose an ERC-721 in your wallet, then pick <strong className="text-neutral-300">Sell (fixed price)</strong> or{" "}
          <strong className="text-neutral-300">Auction</strong>, set your base price (listing price or auction reserve), and
          set how long the listing or auction should run. Fixed-price listings expire on-chain — if nothing sells, you keep
          the NFT and can list again. Auctions use the same on-chain rules as the{" "}
          <Link href="/auctions" className="text-emerald-400 underline">
            Auctions
          </Link>{" "}
          page.
        </p>
        <p className="mt-2 text-xs text-neutral-500">
          Collections shown here are the same set as on your Profile (active listings, favorites, and{" "}
          <span className="font-mono">NEXT_PUBLIC_TRACKED_COLLECTIONS</span>).
        </p>
      </div>

      {!isConnected && (
        <p className="rounded-lg border border-amber-900/50 bg-amber-950/30 px-4 py-3 text-sm text-amber-200/90">
          Connect your wallet in the header to see tokens you own.
        </p>
      )}

      {isConnected && walletPending && (
        <p className="text-sm text-neutral-500">Loading tokens from indexed collections…</p>
      )}

      {isConnected && walletErr && (
        <p className="text-sm text-red-400">{(walletErr as Error).message}</p>
      )}

      {isConnected && !walletPending && walletPack && walletPack.tokens.length === 0 && (
        <div className="rounded-xl border border-dashed border-neutral-700 p-8 text-center text-sm text-neutral-500">
          No ERC-721 balances found. Add your collection to{" "}
          <span className="font-mono text-neutral-400">NEXT_PUBLIC_TRACKED_COLLECTIONS</span> or list a token from the home
          page so the contract address is indexed, then refresh.
        </div>
      )}

      {isConnected && walletPack && walletPack.tokens.length > 0 && (
        <div className="grid gap-6 sm:grid-cols-2 xl:grid-cols-3">
          {walletPack.tokens.map(t => {
            const m = walletPack.meta[t.coll.toLowerCase()];
            return (
              <ListableTokenCard
                key={`${t.coll}-${t.id}`}
                coll={t.coll}
                id={t.id}
                collectionName={m?.name}
                symbol={m?.symbol}
              />
            );
          })}
        </div>
      )}

      <details className="rounded-xl border border-neutral-800 bg-neutral-950/40 p-4 text-sm text-neutral-400">
        <summary className="cursor-pointer font-medium text-neutral-300">Advanced: open by address</summary>
        <p className="mt-2 text-xs">
          If you already know the collection and token ID, open{" "}
          <Link href="/#discover" className="text-emerald-400 underline">
            Browse listings
          </Link>{" "}
          on the home page or go directly to <span className="font-mono text-neutral-500">/token/0x…/id</span>.
        </p>
      </details>

      <section className="rounded-xl border border-neutral-800 p-5 text-sm text-neutral-400">
        <h2 className="mb-2 font-semibold text-neutral-200">ERC-1155</h2>
        <p>
          Multi-edition listings use the same contracts with different entrypoints. This UI focuses on ERC-721 first; use
          contract calls or extend the app for ERC-1155 amounts.
        </p>
      </section>
    </div>
  );
}
