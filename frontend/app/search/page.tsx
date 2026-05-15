"use client";

import {useEffect} from "react";
import {useRouter} from "next/navigation";

/** @deprecated Discovery is on the home page. */
export default function SearchRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/#discover");
  }, [router]);
  return (
    <div className="py-12 text-center text-sm text-neutral-400">
      <p>Redirecting to listings…</p>
    </div>
  );
}
