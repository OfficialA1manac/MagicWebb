"use client";
import {useMemo, useState, useEffect} from "react";
import {useAccount} from "wagmi";
import {type Address, parseEther} from "viem";
import type {Hex} from "viem";
import {useFavorites} from "@/context/FavoritesContext";
import {useWalletHoldings} from "@/hooks/useWalletHoldings";
import {useChainListings} from "@/hooks/useChainListings";
import {useBatchList, BATCH_MAX, type BatchListItem} from "@/hooks/useBatchList";
import {useTx} from "@/hooks/useTx";
import {ProfileNftCard} from "@/components/ProfileNftCard";
import {TxBanner} from "@/components/TxBanner";
import {CURRENCY_SYMBOL} from "@/lib/addresses";
import type {ActiveListing} from "@/lib/marketIndex";

export default function ListNftPage() {
  const {address, isConnected} = useAccount();
  const {favoritesKey} = useFavorites();

  const {data: walletPack, isPending: walletPending, error: walletErr, refetch: refetchWallet} =
    useWalletHoldings(address, favoritesKey);
  const {data: marketData, refetch: refetchListings} = useChainListings();

  const listingLookup = useMemo(() => {
    const m = new Map<string, ActiveListing>();
    if (!marketData?.listings) return m;
    for (const l of marketData.listings) m.set(`${l.coll.toLowerCase()}:${l.id.toString()}`, l);
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

  // ── Batch mode ────────────────────────────────────────────────────────
  const [batchMode, setBatchMode] = useState(false);
  const [selected, setSelected] = useState<Map<string, {coll: Address; id: bigint}>>(new Map());
  const [batchPrice, setBatchPrice] = useState("");
  const [batchDays, setBatchDays] = useState("7");
  const {batchList, isPending: batchPending, error: batchErr} = useBatchList();
  const batchTx = useTx();

  const toggleSelect = (coll: Address, id: bigint) => {
    const k = `${coll.toLowerCase()}:${id.toString()}`;
    setSelected(prev => {
      const next = new Map(prev);
      if (next.has(k)) { next.delete(k); } else if (next.size < BATCH_MAX) { next.set(k, {coll, id}); }
      return next;
    });
  };

  const parsedBatchPrice = (() => {
    try { return batchPrice ? parseEther(batchPrice) : null; } catch { return null; }
  })();
  const batchPriceOk = parsedBatchPrice !== null && parsedBatchPrice > 0n;
  const batchDaysNum = parseInt(batchDays, 10);
  const batchDaysOk = !isNaN(batchDaysNum) && batchDaysNum >= 1 && batchDaysNum <= 365;

  const handleBatchList = async () => {
    if (!batchPriceOk || !batchDaysOk || selected.size === 0) return;
    const expiresAt = BigInt(Math.floor(Date.now() / 1000) + batchDaysNum * 86400);
    const items: BatchListItem[] = [...selected.values()].map(({coll, id}) => ({
      coll, id, price: parsedBatchPrice!, expiresAt,
    }));
    try {
      const h = await batchList(items);
      batchTx.setHash(h as Hex);
    } catch {}
  };

  useEffect(() => {
    if (batchTx.isConfirmed) {
      setSelected(new Map());
      setBatchMode(false);
      void refetchWallet();
      void refetchListings();
    }
  }, [batchTx.isConfirmed, refetchWallet, refetchListings]);

  const refreshAfterAction = () => { void refetchWallet(); void refetchListings(); };

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold md:text-3xl">List an NFT</h1>
          <p className="mt-2 max-w-3xl text-sm text-neutral-400">
            {batchMode
              ? `Select up to ${BATCH_MAX} unlisted tokens, set a shared price and duration, then submit one transaction.`
              : "Set a fixed price and duration per token. For auctions, open the token detail page."}
          </p>
        </div>
        {isConnected && walletPack && walletPack.tokens.length > 0 && (
          <button
            className="shrink-0 rounded border border-neutral-700 px-3 py-1.5 text-sm hover:border-neutral-500"
            onClick={() => { setBatchMode(v => !v); setSelected(new Map()); }}
          >
            {batchMode ? "Cancel batch" : "Batch list"}
          </button>
        )}
      </div>

      {batchMode && (
        <div className="rounded-xl border border-neutral-700 bg-neutral-900/40 p-4 space-y-3">
          <div className="flex items-center justify-between text-sm">
            <span className="font-medium">{selected.size} / {BATCH_MAX} tokens selected</span>
            {selected.size > 0 && (
              <button className="text-xs text-neutral-500 hover:text-neutral-300"
                onClick={() => setSelected(new Map())}>Clear all</button>
            )}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <label className="block text-sm">
              Price per token ({CURRENCY_SYMBOL})
              <input
                className="mt-1 w-full rounded border border-neutral-700 bg-neutral-950 px-2 py-1.5 text-xs"
                placeholder="e.g. 1.5"
                value={batchPrice}
                onChange={e => setBatchPrice(e.target.value)}
              />
              {batchPrice && !batchPriceOk && (
                <p className="text-xs text-red-400 mt-0.5">Enter a valid price &gt; 0</p>
              )}
            </label>
            <label className="block text-sm">
              Duration (days)
              <select
                className="mt-1 w-full rounded border border-neutral-700 bg-neutral-950 px-2 py-1.5 text-xs"
                value={batchDays}
                onChange={e => setBatchDays(e.target.value)}
              >
                {(["1","3","7","30","90"] as const).map(v => (
                  <option key={v} value={v}>{v === "1" ? "1 day" : `${v} days`}</option>
                ))}
              </select>
            </label>
          </div>
          <button
            className="w-full rounded bg-emerald-600 hover:bg-emerald-500 px-4 py-2 text-sm font-medium disabled:opacity-50"
            disabled={selected.size === 0 || !batchPriceOk || !batchDaysOk || batchPending || batchTx.isConfirming}
            onClick={handleBatchList}
          >
            {batchPending ? "Confirm in wallet…"
              : batchTx.isConfirming ? "Listing…"
              : `List ${selected.size} token${selected.size !== 1 ? "s" : ""}`}
          </button>
          <TxBanner
            hash={batchTx.hash}
            isConfirming={batchTx.isConfirming}
            isConfirmed={batchTx.isConfirmed}
            error={(batchErr as Error | null) ?? batchTx.txError}
            label="Batch list"
          />
          {selected.size === BATCH_MAX && (
            <p className="text-xs text-amber-400/80">Maximum {BATCH_MAX} tokens per transaction reached.</p>
          )}
        </div>
      )}

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
          No ERC-721 tokens found in indexed collections.
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
                batchMode={batchMode}
                selected={selected.has(k)}
                onToggleSelect={toggleSelect}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
