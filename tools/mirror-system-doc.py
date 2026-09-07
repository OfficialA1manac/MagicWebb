#!/usr/bin/env python3
"""Regenerate app/src/pages/docs/system.md from docs/SYSTEM_BREAKDOWN.md.

The app copy carries Astro frontmatter (DocLayout, `wide: true`), keeps every
section and diagram, and replaces the screenshot table with a pointer to the
GitHub file (the images live in the repository, not in the app bundle).

    python tools/mirror-system-doc.py
"""
from __future__ import annotations

import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parents[1]
SRC = ROOT / "docs" / "SYSTEM_BREAKDOWN.md"
OUT = ROOT / "app" / "src" / "pages" / "docs" / "system.md"
GITHUB = "https://github.com/OfficialA1manac/MagicWebb/blob/main/docs/SYSTEM_BREAKDOWN.md"

SELF_NOTE = (
    "`app/src/pages/docs/system.md` mirrors this file minus the\n"
    "screenshot section — edit both, or regenerate the mirror\n"
    "(`python tools/mirror-system-doc.py`)."
)
APP_NOTE = (
    "This page is generated from\n"
    f"[`docs/SYSTEM_BREAKDOWN.md`]({GITHUB}) (`python tools/mirror-system-doc.py`);\n"
    "the screenshot gallery lives there."
)
FRONTMATTER = (
    "---\n"
    "layout: ../../layouts/DocLayout.astro\n"
    'title: "System Breakdown"\n'
    'description: "Every mechanism drawn out: deployment, request path, indexer and keeper loop, state machines, roles, governance, badges."\n'
    "wide: true\n"
    "---\n\n"
)


def main() -> None:
    src = SRC.read_text(encoding="utf-8")
    assert SELF_NOTE in src, "intro mirror note not found — keep the wording in sync"
    body = src.replace(SELF_NOTE, APP_NOTE)

    m = re.search(r"^## 10\. The product.*?(?=^## 11\. )", body, re.S | re.M)
    assert m, "screenshot section (## 10.) not found"
    pointer = (
        "## 10. The product (screenshots)\n\n"
        "Desktop and phone, light and dark, for every page — captured from the live app with\n"
        "`npm run shots` and kept in the repository:\n"
        f"[docs/SYSTEM_BREAKDOWN.md §10]({GITHUB}#10-the-product-v36-screenshots).\n\n"
    )
    body = body[: m.start()] + pointer + body[m.end() :]

    OUT.write_text(FRONTMATTER + body, encoding="utf-8", newline="\n")
    print("wrote", OUT.relative_to(ROOT))


if __name__ == "__main__":
    main()
