import {isAddress, type Address} from "viem";

const STORAGE_KEY = "magicwebb:favorites:v1";

export type FavoriteEntry = {coll: Address; id: bigint};

function parseEntry(s: string): FavoriteEntry | null {
  const idx = s.lastIndexOf(":");
  if (idx <= 2) return null;
  const coll = s.slice(0, idx) as Address;
  const idPart = s.slice(idx + 1);
  if (!isAddress(coll)) return null;
  try {
    return {coll, id: BigInt(idPart)};
  } catch {
    return null;
  }
}

export function readFavoritesFromStorage(): FavoriteEntry[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw) as unknown;
    if (!Array.isArray(arr)) return [];
    const out: FavoriteEntry[] = [];
    for (const x of arr) {
      if (typeof x !== "string") continue;
      const e = parseEntry(x);
      if (e) out.push(e);
    }
    return out;
  } catch {
    return [];
  }
}

export function writeFavorites(entries: FavoriteEntry[]) {
  const serial = entries.map(e => `${e.coll.toLowerCase()}:${e.id.toString()}`);
  localStorage.setItem(STORAGE_KEY, JSON.stringify(serial));
}

function keyOf(coll: Address, id: bigint) {
  return `${coll.toLowerCase()}:${id.toString()}`;
}

export function isFavorite(coll: Address, id: bigint): boolean {
  return readFavoritesFromStorage().some(e => keyOf(e.coll, e.id) === keyOf(coll, id));
}

/** Returns the new favorites list after toggle. */
export function toggleFavorite(coll: Address, id: bigint): FavoriteEntry[] {
  const cur = readFavoritesFromStorage();
  const k = keyOf(coll, id);
  const i = cur.findIndex(e => keyOf(e.coll, e.id) === k);
  let next: FavoriteEntry[];
  if (i >= 0) {
    next = [...cur];
    next.splice(i, 1);
  } else {
    next = [...cur, {coll, id}];
  }
  writeFavorites(next);
  return next;
}

export function removeFavorite(coll: Address, id: bigint): FavoriteEntry[] {
  const k = keyOf(coll, id);
  const next = readFavoritesFromStorage().filter(e => keyOf(e.coll, e.id) !== k);
  writeFavorites(next);
  return next;
}
