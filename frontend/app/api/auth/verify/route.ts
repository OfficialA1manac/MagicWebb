import {NextRequest, NextResponse} from "next/server";
import {randomBytes} from "crypto";
import {recoverMessageAddress} from "viem";
import {nonces} from "../nonce/route";

export const dynamic = "force-dynamic";

function extractField(message: string, field: string): string | null {
  const prefix = `${field}: `;
  for (const line of message.split("\n")) {
    if (line.startsWith(prefix)) return line.slice(prefix.length).trim();
  }
  return null;
}

// Session store — swap for Redis/Supabase in production.
export const sessions = new Map<string, {wallet: string; expires: number}>();

export async function POST(req: NextRequest) {
  let body: {message?: string; signature?: string};
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({error: "invalid json"}, {status: 400});
  }

  const {message, signature} = body;
  if (!message || !signature) {
    return NextResponse.json({error: "missing message or signature"}, {status: 400});
  }

  const nonce = extractField(message, "Nonce");
  if (!nonce) return NextResponse.json({error: "missing nonce"}, {status: 400});

  const expiry = nonces.get(nonce);
  if (!expiry || expiry < Date.now()) {
    return NextResponse.json({error: "nonce expired or used"}, {status: 401});
  }
  nonces.delete(nonce);

  let recovered: string;
  try {
    recovered = await recoverMessageAddress({
      message,
      signature: signature as `0x${string}`,
    });
  } catch {
    return NextResponse.json({error: "invalid signature"}, {status: 401});
  }

  const lines = message.split("\n");
  const claimedAddr = lines[1]?.trim().toLowerCase() ?? "";
  if (recovered.toLowerCase() !== claimedAddr) {
    return NextResponse.json({error: "address mismatch"}, {status: 401});
  }

  const token = randomBytes(32).toString("hex");
  sessions.set(token, {wallet: recovered.toLowerCase(), expires: Date.now() + 7 * 86400_000});

  return NextResponse.json({token, wallet: recovered.toLowerCase()});
}
