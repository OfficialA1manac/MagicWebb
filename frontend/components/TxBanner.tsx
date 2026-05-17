"use client";
import type {Hex} from "viem";
import {humanizeTxError} from "@/lib/txErrors";
import {EXPLORER_URL} from "@/lib/addresses";

export function TxBanner({
  hash, isConfirming, isConfirmed, error, label = "Transaction"
}: {
  hash?: Hex;
  isConfirming?: boolean;
  isConfirmed?: boolean;
  error?: Error | null;
  label?: string;
}) {
  if (!hash && !error) return null;
  const explorer = hash ? `${EXPLORER_URL}/tx/${hash}` : undefined;
  if (error) {
    const raw = error.message.split("\n")[0];
    const hint = humanizeTxError(error.message);
    return (
      <div className="rounded border border-red-700 bg-red-900/30 p-2 text-sm text-red-300 break-words space-y-1">
        <div>
          {label} failed: {raw}
        </div>
        {hint && <div className="text-xs text-red-200/90 border-t border-red-800/60 pt-1">{hint}</div>}
      </div>
    );
  }
  return (
    <div
      className={`rounded border p-2 text-sm break-words ${
        isConfirmed
          ? "border-emerald-700 bg-emerald-900/30 text-emerald-300"
          : "border-blue-700 bg-blue-900/30 text-blue-300"
      }`}
    >
      {isConfirming && !isConfirmed && <>{label} pending…</>}
      {isConfirmed && <>{label} confirmed.</>}
      {explorer && (
        <>
          {" "}
          <a className="underline" href={explorer} target="_blank" rel="noreferrer">
            view tx
          </a>
        </>
      )}
    </div>
  );
}
