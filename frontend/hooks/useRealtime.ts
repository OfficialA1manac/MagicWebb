"use client";
import {useEffect, useRef} from "react";
import {useQueryClient} from "@tanstack/react-query";

const BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

type SSEEvent = {
  type: string;
  [key: string]: unknown;
};

export function useRealtime(topics: string[]) {
  const qc = useQueryClient();
  const topicsRef = useRef(topics);
  topicsRef.current = topics;

  useEffect(() => {
    let dead = false;
    let reconnectTimer: ReturnType<typeof setTimeout>;
    let es: EventSource | null = null;

    function connect() {
      if (dead) return;

      const params = new URLSearchParams();
      for (const topic of topicsRef.current) {
        params.append("topic", topic);
      }

      try {
        es = new EventSource(`${BASE}/events?${params.toString()}`);
      } catch {
        reconnectTimer = setTimeout(connect, 3000);
        return;
      }

      es.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data as string) as SSEEvent;
          const eventType = msg.type ?? "";

          if (
            eventType === "Listed" ||
            eventType === "Bought" ||
            eventType === "Cancelled"
          ) {
            qc.invalidateQueries({queryKey: ["chain-listings"]});
            qc.invalidateQueries({queryKey: ["trending"]});
          } else if (
            eventType === "AuctionCreated" ||
            eventType === "BidPlaced" ||
            eventType === "AuctionSettled"
          ) {
            qc.invalidateQueries({queryKey: ["chain-auctions"]});
            const id = msg.auction_id as string | undefined;
            if (id) qc.invalidateQueries({queryKey: ["auction", id]});
          } else if (
            eventType === "OfferCreated" ||
            eventType === "OfferAccepted" ||
            eventType === "OfferCancelled"
          ) {
            qc.invalidateQueries({queryKey: ["offers"]});
          }
        } catch {}
      };

      es.onerror = () => {
        es?.close();
        es = null;
        if (!dead) reconnectTimer = setTimeout(connect, 3000);
      };
    }

    connect();
    return () => {
      dead = true;
      clearTimeout(reconnectTimer);
      es?.close();
    };
  }, [qc]);
}
