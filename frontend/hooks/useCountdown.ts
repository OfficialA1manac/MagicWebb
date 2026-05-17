"use client";
import {useEffect, useState} from "react";
import {useServerTime} from "./useServerTime";

export function useCountdown(endsAtSeconds: bigint) {
  const {nowSeconds} = useServerTime();
  const [remaining, setRemaining] = useState(() =>
    Math.max(0, Number(endsAtSeconds) - Number(nowSeconds()))
  );

  useEffect(() => {
    const tick = () => {
      setRemaining(Math.max(0, Number(endsAtSeconds) - Number(nowSeconds())));
    };
    tick();
    const t = setInterval(tick, 1000);
    return () => clearInterval(t);
  }, [endsAtSeconds, nowSeconds]);

  const h = Math.floor(remaining / 3600);
  const m = Math.floor((remaining % 3600) / 60);
  const s = remaining % 60;

  const formatted = remaining <= 0
    ? "Ended"
    : h > 0
      ? `${h}h ${m}m ${s}s`
      : m > 0
        ? `${m}m ${s}s`
        : `${s}s`;

  return {remaining, formatted, ended: remaining <= 0};
}
