#!/usr/bin/env python3
"""Fill deployments/<network>.json from the DeployV34 forge broadcast.

    python tools/record-deployment.py <songbird|flare|coston2> [--nft 0x…] [--track 0x…,0x…]

Reads contracts/broadcast/DeployV34.s.sol/<chainId>/run-latest.json (the file
`forge script … --broadcast` writes), maps the eight CREATEs to the record
(v3.7: four implementations + four ERC-1967 proxies — manager first), sets status
"deployed", deployedAt (today, UTC), indexFromBlock (the first receipt's block)
and impls, moves any previous contract set into `superseded`, and leaves nft /
trackedCollections as given (null / [] for a mainnet with no seed collection).
Then run tools/check-deployments.sh and commit.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
CHAIN = {"coston2": 114, "songbird": 19, "flare": 14}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("network", choices=sorted(CHAIN))
    ap.add_argument("--nft", default=None, help="seed / flagship collection address (optional)")
    ap.add_argument("--track", default="", help="comma-separated tracked collection addresses")
    ap.add_argument("--broadcast", default=None, help="path to run-latest.json (default: forge's)")
    a = ap.parse_args()

    chain = CHAIN[a.network]
    bpath = pathlib.Path(a.broadcast) if a.broadcast else ROOT / "contracts" / "broadcast" / "DeployV34.s.sol" / str(chain) / "run-latest.json"
    if not bpath.exists():
        print(f"no broadcast at {bpath} — run forge script … --broadcast first", file=sys.stderr)
        return 1
    b = json.loads(bpath.read_text(encoding="utf-8"))
    if int(b.get("chain", chain)) != chain:
        print(f"broadcast chain {b.get('chain')} != {chain}", file=sys.stderr)
        return 1

    creates = [t for t in b["transactions"] if t.get("transactionType") == "CREATE"]
    names = [t.get("contractName") for t in creates]
    expect = ["MarketplaceManager", "ERC1967Proxy", "Marketplace", "ERC1967Proxy", "AuctionHouse", "ERC1967Proxy", "OfferBook", "ERC1967Proxy"]
    if names != expect:
        print(f"unexpected CREATE order {names}; expected {expect}", file=sys.stderr)
        return 1
    addr = [t["contractAddress"] for t in creates]
    mgr_impl, manager, mp_impl, mp, ah_impl, ah, ob_impl, ob = addr
    # The proxy constructor's first argument is the implementation it wraps —
    # cross-check so a reordered broadcast can never be recorded wrong.
    for proxy_tx, impl in ((creates[1], mgr_impl), (creates[3], mp_impl), (creates[5], ah_impl), (creates[7], ob_impl)):
        arg0 = (proxy_tx.get("arguments") or [""])[0]
        if arg0.lower() != impl.lower():
            print(f"proxy {proxy_tx['contractAddress']} wraps {arg0}, expected {impl}", file=sys.stderr)
            return 1
    blocks = sorted(int(r["blockNumber"], 16) for r in b["receipts"])
    first_block = blocks[0]

    dpath = ROOT / "deployments" / f"{a.network}.json"
    d = json.loads(dpath.read_text(encoding="utf-8"))
    old = d.get("contracts") or {}
    cores = ("marketplace", "auctionHouse", "offerBook", "marketplaceManager")
    new_set = {"marketplace": mp, "auctionHouse": ah, "offerBook": ob, "marketplaceManager": manager}
    changed = any((old.get(k) or "").lower() != new_set[k].lower() for k in cores)
    if any(old.get(k) for k in cores) and changed:
        # Archive the previous set only when an address actually changes; a
        # re-run for --track / --nft on the same broadcast keeps the record as is.
        d.setdefault("superseded", []).insert(0, {
            "note": f"replaced {dt.date.today().isoformat()} by the v3.7 DeployV34 set at block {first_block}",
            **{k: old.get(k) for k in ("marketplace", "auctionHouse", "offerBook", "marketplaceManager", "nft")},
        })
    d["status"] = "deployed"
    if changed or not d.get("deployedAt"):
        d["deployedAt"] = dt.datetime.now(dt.timezone.utc).date().isoformat()
    if changed or not d.get("indexFromBlock"):
        d["indexFromBlock"] = first_block
    d["contracts"] = {
        "marketplace": mp,
        "auctionHouse": ah,
        "offerBook": ob,
        "marketplaceManager": manager,
        "nft": a.nft or old.get("nft"),
    }
    if changed or not d.get("impls"):
        d["impls"] = {"marketplace": mp_impl, "auctionHouse": ah_impl, "offerBook": ob_impl, "marketplaceManager": mgr_impl, "upgradedAt": d["deployedAt"]}
    tracked = [x.strip() for x in a.track.split(",") if x.strip()]
    d["trackedCollections"] = tracked if tracked else (d.get("trackedCollections") or [])
    d.pop("note", None)
    dpath.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"{dpath.relative_to(ROOT)}: status deployed, indexFromBlock {first_block}")
    print(f"  manager {manager} (impl {mgr_impl})\n  marketplace {mp} (impl {mp_impl})\n  auctionHouse {ah} (impl {ah_impl})\n  offerBook {ob} (impl {ob_impl})")
    print("next: bash tools/check-deployments.sh && git add deployments && git commit && git push")
    return 0


if __name__ == "__main__":
    sys.exit(main())
