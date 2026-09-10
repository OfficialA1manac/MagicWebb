#!/usr/bin/env python3
"""Storage-layout gate for the UUPS proxies (v3.7 CSO finding, verified 9/10).

    python tools/check-storage-layout.py            # compare against the committed baselines
    python tools/check-storage-layout.py --update   # (re)write the baselines from the current build

Upgrades are instant (upgradeDelay()==0) and the proxies hold user escrow, so
an implementation whose storage layout drifted (a base contract inserted or
reordered, a variable moved) would make every mapping read zero the moment it
is installed. This script runs `forge inspect <C> storage-layout --json` for
each proxied contract and compares it with contracts/storage-layout/<C>.json:

  - every baseline variable must keep its slot, offset and type;
  - new variables may only be APPENDED (a higher slot than every baseline
    variable) or take slots freed by shrinking a `__gap` (the gap's own type
    may shrink, e.g. uint256[49] -> uint256[48], never grow or move);
  - nothing may disappear.

CI runs it after `forge build`; tools/upgrade-cores.sh runs it before it deploys
new implementations; a fresh network (DeployV34) needs no check but the
baselines must match what is committed. Type ids strip solc's AST ids so a
rebuild does not produce spurious diffs.
"""
from __future__ import annotations

import json
import os
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts"
BASELINES = CONTRACTS / "storage-layout"
PROXIED = ["Marketplace", "AuctionHouse", "OfferBook", "MarketplaceManager"]


def norm_type(t: str) -> str:
    # Strip solc AST ids ONLY from struct/contract/enum names — never the
    # length of an array type: t_struct(Listing)1234_storage ->
    # t_struct(Listing)_storage, t_contract(IERC20)55 -> t_contract(IERC20),
    # but t_array(t_uint256)49_storage keeps its 49 (that is the gap length).
    return re.sub(r"(t_(?:struct|contract|enum)\([^)]*\))\d+", r"\1", t)


def inspect(name: str) -> list[dict]:
    # The `layout` profile (foundry.toml) compiles src/ only, into its own
    # out/cache dirs: storage layout does not depend on tests or scripts, and
    # the full via-IR build of the test suite does not fit an 8 GB machine
    # next to anything else. Same solc, same via-IR + optimizer as the deploy.
    env = dict(os.environ, FOUNDRY_PROFILE="layout")
    out = subprocess.run(
        ["forge", "inspect", name, "storage-layout", "--json"],
        cwd=CONTRACTS, capture_output=True, text=True, check=False, env=env,
    )
    if out.returncode != 0:
        print(out.stderr.strip(), file=sys.stderr)
        raise SystemExit(f"forge inspect {name} failed")
    data = json.loads(out.stdout)
    storage = data["storage"] if isinstance(data, dict) else data
    return [
        {"label": s["label"], "slot": int(s["slot"]), "offset": int(s["offset"]), "type": norm_type(s["type"])}
        for s in storage
    ]


def is_gap(label: str) -> bool:
    return label.endswith("__gap") or "gap" in label.lower()


def gap_len(t: str) -> int | None:
    m = re.fullmatch(r"t_array\(t_uint256\)(\d+)_storage", t)
    return int(m.group(1)) if m else None


def compare(name: str, base: list[dict], cur: list[dict]) -> list[str]:
    problems: list[str] = []
    cur_by_key = {(v["label"], v["slot"], v["offset"]): v for v in cur}
    cur_by_label = {}
    for v in cur:
        cur_by_label.setdefault(v["label"], []).append(v)
    max_base_slot = max((v["slot"] for v in base), default=-1)
    for b in base:
        key = (b["label"], b["slot"], b["offset"])
        c = cur_by_key.get(key)
        if c is None:
            where = cur_by_label.get(b["label"])
            if where:
                problems.append(f"{name}: `{b['label']}` moved from slot {b['slot']}/{b['offset']} to "
                                + ", ".join(f"{w['slot']}/{w['offset']}" for w in where))
            else:
                problems.append(f"{name}: `{b['label']}` (slot {b['slot']}) disappeared")
            continue
        if c["type"] != b["type"]:
            gb, gc = gap_len(b["type"]), gap_len(c["type"])
            if is_gap(b["label"]) and gb is not None and gc is not None and gc <= gb:
                continue  # a gap consumed by appended variables — allowed
            problems.append(f"{name}: `{b['label']}` type changed {b['type']} -> {c['type']}")
    base_keys = {(v["label"], v["slot"], v["offset"]) for v in base}
    for c in cur:
        key = (c["label"], c["slot"], c["offset"])
        if key in base_keys:
            continue
        # New variable: must sit above every baseline slot, or inside a shrunken gap.
        if c["slot"] > max_base_slot:
            continue
        inside_gap = any(
            is_gap(b["label"]) and b["slot"] < c["slot"] <= b["slot"] + (gap_len(b["type"]) or 0)
            for b in base
        )
        if not inside_gap:
            problems.append(f"{name}: new variable `{c['label']}` at slot {c['slot']} is not appended "
                            "(it sits inside the existing layout)")
    return problems


def main() -> int:
    update = "--update" in sys.argv[1:]
    BASELINES.mkdir(exist_ok=True)
    failures: list[str] = []
    for name in PROXIED:
        cur = inspect(name)
        path = BASELINES / f"{name}.json"
        if update:
            path.write_text(json.dumps(cur, indent=2) + "\n", encoding="utf-8")
            print(f"updated {path.relative_to(ROOT)} ({len(cur)} variables)")
            continue
        if not path.exists():
            # A missing baseline must never pass silently (CI would otherwise
            # green-light an unchecked layout); create it deliberately.
            failures.append(f"{name}: no baseline at {path.relative_to(ROOT)} — run with --update and commit it")
            continue
        base = json.loads(path.read_text(encoding="utf-8"))
        problems = compare(name, base, cur)
        if problems:
            failures.extend(problems)
        else:
            print(f"{name}: layout OK ({len(cur)} variables, {len(base)} baseline)")
    if failures:
        print("\nSTORAGE LAYOUT DRIFT — installing this implementation on a live proxy would corrupt state:", file=sys.stderr)
        for p in failures:
            print(f"  - {p}", file=sys.stderr)
        print("\nIf this is a fresh deployment of a NEW layout (never on chain), run with --update and commit the baselines.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
