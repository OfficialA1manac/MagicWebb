"use client";
import {useEffect, useRef} from "react";
import {useQueryClient} from "@tanstack/react-query";

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:4001/ws";

type WSMessage = {
  type: string;
  topic: string;
  payload: unknown;
};

export function useRealtime(topics: string[]) {
  const qc = useQueryClient();
  const topicsRef = useRef(topics);
  topicsRef.current = topics;

  useEffect(() => {
    let dead = false;
    let reconnectTimer: ReturnType<typeof setTimeout>;

    function connect() {
      if (dead) return;
      let socket: WebSocket;
      try {
        socket = new WebSocket(WS_URL);
      } catch {
        reconnectTimer = setTimeout(connect, 3000);
        return;
      }

      socket.onopen = () => {
        for (const topic of topicsRef.current) {
          socket.send(JSON.stringify({type: "subscribe", topic}));
        }
      };

      socket.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data as string) as WSMessage;
          if (msg.type !== "event") return;

          if (msg.topic === "listings") {
            qc.invalidateQueries({queryKey: ["chain-listings"]});
            qc.invalidateQueries({queryKey: ["trending"]});
          } else if (msg.topic.startsWith("auction")) {
            const parts = msg.topic.split(":");
            qc.invalidateQueries({queryKey: ["chain-auctions"]});
            if (parts[1]) qc.invalidateQueries({queryKey: ["auction", parts[1]]});
          } else if (msg.topic === "offers") {
            qc.invalidateQueries({queryKey: ["offers"]});
          }
        } catch {}
      };

      socket.onclose = () => {
        if (!dead) reconnectTimer = setTimeout(connect, 3000);
      };
    }

    connect();
    return () => {
      dead = true;
      clearTimeout(reconnectTimer);
    };
  }, [qc]);
}
