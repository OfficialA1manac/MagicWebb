"use client";
import {Suspense, useState} from "react";
import {useSearchParams} from "next/navigation";
import {useQuery} from "@tanstack/react-query";
import {useAccount} from "wagmi";
import {SentOfferCard} from "@/components/SentOfferCard";
import {ReceivedOfferCard} from "@/components/ReceivedOfferCard";
import {api, type BackendOffer} from "@/lib/api";

function OffersContent() {
  const searchParams = useSearchParams();
  const [tab, setTab] = useState<"sent" | "received">(() =>
    searchParams.get("tab") === "received" ? "received" : "sent"
  );
  const {address} = useAccount();
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());

  const {data: sent = [], refetch: refetchSent} = useQuery<BackendOffer[]>({
    queryKey: ["offers", "sent", address],
    queryFn: () => api.getOffers({bidder: address}) as Promise<BackendOffer[]>,
    enabled: !!address,
    staleTime: 15_000,
  });

  const {data: received = [], refetch: refetchReceived} = useQuery<BackendOffer[]>({
    queryKey: ["offers", "received", address],
    queryFn: () => api.getOffers({owner: address}) as Promise<BackendOffer[]>,
    enabled: !!address,
    staleTime: 15_000,
  });

  const visibleSent = sent.filter(o => !dismissed.has(o.offer_id));
  const visibleReceived = received.filter(o => !dismissed.has(o.offer_id));

  const dismiss = (id: string) => setDismissed(s => new Set([...s, id]));

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-bold md:text-3xl">Offers</h1>
        <p className="mt-2 max-w-3xl text-sm text-neutral-400">
          EIP-712 signed offers submitted to the backend.{" "}
          <strong className="text-neutral-300">Sent</strong> shows offers you signed;{" "}
          <strong className="text-neutral-300">Received</strong> shows offers on tokens you own.
        </p>
      </div>

      {!address && (
        <p className="text-sm text-neutral-500">Connect your wallet to view offers.</p>
      )}

      <div className="flex flex-wrap gap-2 border-b border-neutral-800 pb-1">
        <button
          type="button"
          className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
            tab === "sent" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
          }`}
          onClick={() => setTab("sent")}
        >
          Offers sent
        </button>
        <button
          type="button"
          className={`rounded-t-lg px-4 py-2 text-sm font-medium ${
            tab === "received" ? "bg-emerald-950/50 text-emerald-200" : "text-neutral-400 hover:text-neutral-200"
          }`}
          onClick={() => setTab("received")}
        >
          Offers received
        </button>
      </div>

      {tab === "sent" && (
        <section className="space-y-4">
          {visibleSent.length === 0 ? (
            <p className="text-sm text-neutral-500">
              {address ? "No sent offers." : "Connect wallet to view."}
            </p>
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {visibleSent.map(o => (
                <SentOfferCard
                  key={o.offer_id}
                  offer={o}
                  onChanged={() => { dismiss(o.offer_id); refetchSent(); }}
                />
              ))}
            </div>
          )}
        </section>
      )}

      {tab === "received" && (
        <section className="space-y-4">
          {visibleReceived.length === 0 ? (
            <p className="text-sm text-neutral-500">
              {address ? "No received offers." : "Connect wallet to view."}
            </p>
          ) : (
            <div className="grid gap-4 md:grid-cols-2">
              {visibleReceived.map(o => (
                <ReceivedOfferCard
                  key={o.offer_id}
                  offer={o}
                  onChanged={() => { dismiss(o.offer_id); refetchReceived(); }}
                />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

export default function OffersPage() {
  return (
    <Suspense fallback={<div className="text-sm text-neutral-500">Loading offers…</div>}>
      <OffersContent />
    </Suspense>
  );
}
