"use client";
import type {Hex} from "viem";

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
  const explorer = hash ? `https://coston2-explorer.flare.network/tx/${hash}` : undefined;
  if (error) {
    return (
      <div className="rounded border border-red-700 bg-red-900/30 p-2 text-sm text-red-300 break-words">
        {label} failed: {error.message.split("\n")[0]}
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
