import { createClient, cacheExchange, fetchExchange, subscriptionExchange } from "urql";
import { createClient as createWsClient } from "graphql-ws";

const wsClient = createWsClient({
  url: process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8080/graphql",
});

export const urqlClient = createClient({
  url: (process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080") + "/graphql",
  exchanges: [
    cacheExchange,
    subscriptionExchange({
      forwardSubscription: (op) => ({
        subscribe: (sink) => ({ unsubscribe: wsClient.subscribe(op, sink) }),
      }),
    }),
    fetchExchange,
  ],
});
