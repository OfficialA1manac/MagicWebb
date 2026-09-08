#!/usr/bin/env python3
"""Fill deployments/<network>.json from the DeployV34 forge broadcast.

    python tools/record-deployment.py <songbird|flare|coston2> [--nft 0x…] [--track 0x…,0x…]

Reads contracts/broadcast/DeployV34.s.sol/<chainId>/run-latest.json (the file
`forge script … --broadcast` writes), maps the seven CREATEs to the record
(manager, three implementations, three ERC-1967 proxies), sets status
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
    expect = ["MarketplaceManager", "Marketplace", "ERC1967Proxy", "AuctionHouse", "ERC1967Proxy", "OfferBook", "ERC1967Proxy"]
    if names != expect:
        print(f"unexpected CREATE order {names}; expected {expect}", file=sys.stderr)
        return 1
    addr = [t["contractAddress"] for t in creates]
    manager, mp_impl, mp, ah_impl, ah, ob_impl, ob = addr
    # The proxy constructor's first argument is the implementation it wraps —
    # cross-check so a reordered broadcast can never be recorded wrong.
    for proxy_tx, impl in ((creates[2], mp_impl), (creates[4], ah_impl), (creates[6], ob_impl)):
        arg0 = (proxy_tx.get("arguments") or [""])[0]
        if arg0.lower() != impl.lower():
            print(f"proxy {proxy_tx['contractAddress']} wraps {arg0}, expected {impl}", file=sys.stderr)
            return 1
    blocks = sorted(int(r["blockNumber"], 16) for r in b["receipts"])
    first_block = blocks[0]

    dpath = ROOT / "deployments" / f"{a.network}.json"
    d = json.loads(dpath.read_text(encoding="utf-8"))
    old = d.get("contracts") or {}
    if any(old.get(k) for k in ("marketplace", "auctionHouse", "offerBook", "marketplaceManager")):
        d.setdefault("superseded", []).insert(0, {
            "note": f"replaced {dt.date.today().isoformat()} by the v3.6 DeployV34 set at block {first_block}",
            **{k: old.get(k) for k in ("marketplace", "auctionHouse", "offerBook", "marketplaceManager", "nft")},
        })
    d["status"] = "deployed"
    d["deployedAt"] = dt.datetime.now(dt.timezone.utc).date().isoformat()
    d["indexFromBlock"] = first_block
    d["contracts"] = {
        "marketplace": mp,
        "auctionHouse": ah,
        "offerBook": ob,
        "marketplaceManager": manager,
        "nft": a.nft or old.get("nft"),
    }
    d["impls"] = {"marketplace": mp_impl, "auctionHouse": ah_impl, "offerBook": ob_impl, "upgradedAt": d["deployedAt"]}
    tracked = [x.strip() for x in a.track.split(",") if x.strip()]
    d["trackedCollections"] = tracked if tracked else (d.get("trackedCollections") or [])
    d.pop("note", None)
    dpath.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"{dpath.relative_to(ROOT)}: status deployed, indexFromBlock {first_block}")
    print(f"  manager {manager}\n  marketplace {mp} (impl {mp_impl})\n  auctionHouse {ah} (impl {ah_impl})\n  offerBook {ob} (impl {ob_impl})")
    print("next: bash tools/check-deployments.sh && git add deployments && git commit && git push")
    return 0


if __name__ == "__main__":
    sys.exit(main())
