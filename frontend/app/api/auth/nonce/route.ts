import {NextResponse} from "next/server";
import {randomBytes} from "crypto";

export const dynamic = "force-dynamic";

// Nonces stored in-memory for dev; swap for Upstash Redis in production.
const nonces = new Map<string, number>();

export function GET() {
  const nonce = randomBytes(16).toString("hex");
  nonces.set(nonce, Date.now() + 600_000); // 10-minute TTL
  // Prune expired nonces.
  const now = Date.now();
  for (const [k, exp] of nonces) {
    if (exp < now) nonces.delete(k);
  }
  return NextResponse.json({nonce});
}

export {nonces};
