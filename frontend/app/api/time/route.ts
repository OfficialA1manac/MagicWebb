import {NextResponse} from "next/server";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export function GET() {
  return NextResponse.json(
    {now: Math.floor(Date.now() / 1000)},
    {headers: {"Cache-Control": "no-store"}}
  );
}
