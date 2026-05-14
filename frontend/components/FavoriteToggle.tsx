"use client";
import {type Address} from "viem";
import {useFavorites} from "@/context/FavoritesContext";

export function FavoriteToggle({coll, id}: {coll: Address; id: bigint}) {
  const {toggle, isFav} = useFavorites();
  const on = isFav(coll, id);
  return (
    <button
      type="button"
      className={`rounded-full border px-2 py-1 text-xs transition ${
        on
          ? "border-rose-500/60 bg-rose-950/50 text-rose-300"
          : "border-neutral-600 bg-neutral-950/80 text-neutral-400 hover:border-emerald-600/50"
      }`}
      onClick={() => toggle(coll, id)}
      aria-pressed={on}
      aria-label={on ? "Remove from favorites" : "Save as favorite"}
    >
      {on ? "★" : "☆"}
    </button>
  );
}
