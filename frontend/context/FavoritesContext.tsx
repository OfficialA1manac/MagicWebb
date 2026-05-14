"use client";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode
} from "react";
import {type Address} from "viem";
import {
  readFavoritesFromStorage,
  writeFavorites,
  type FavoriteEntry
} from "@/lib/favorites";

function keyOf(coll: Address, id: bigint) {
  return `${coll.toLowerCase()}:${id.toString()}`;
}

type Ctx = {
  items: FavoriteEntry[];
  toggle: (coll: Address, id: bigint) => void;
  isFav: (coll: Address, id: bigint) => boolean;
  favoritesKey: string;
};

const FavoritesContext = createContext<Ctx | null>(null);

export function FavoritesProvider({children}: {children: ReactNode}) {
  const [items, setItems] = useState<FavoriteEntry[]>([]);

  useEffect(() => {
    setItems(readFavoritesFromStorage());
  }, []);

  const toggle = useCallback((coll: Address, id: bigint) => {
    setItems(prev => {
      const k = keyOf(coll, id);
      const i = prev.findIndex(e => keyOf(e.coll, e.id) === k);
      const next =
        i >= 0 ? [...prev.slice(0, i), ...prev.slice(i + 1)] : [...prev, {coll, id}];
      writeFavorites(next);
      return next;
    });
  }, []);

  const isFav = useCallback(
    (coll: Address, id: bigint) =>
      items.some(e => keyOf(e.coll, e.id) === keyOf(coll, id)),
    [items]
  );

  const favoritesKey = useMemo(
    () =>
      [...items]
        .map(e => keyOf(e.coll, e.id))
        .sort()
        .join("|"),
    [items]
  );

  const value = useMemo(
    () => ({items, toggle, isFav, favoritesKey}),
    [items, toggle, isFav, favoritesKey]
  );

  return <FavoritesContext.Provider value={value}>{children}</FavoritesContext.Provider>;
}

export function useFavorites() {
  const v = useContext(FavoritesContext);
  if (!v) throw new Error("useFavorites must be used inside FavoritesProvider");
  return v;
}
