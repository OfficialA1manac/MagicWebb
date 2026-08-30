# What users can and cannot do

**This document has moved. The canonical source is
[`app/src/pages/docs/capabilities.md`](../app/src/pages/docs/capabilities.md),
which is what ships to users at `/docs/capabilities`.**

## Why this is a stub

Two full copies of this document existed and drifted apart. The copy that
users actually read had gone stale in ways the repo copy had not: it claimed
*anyone* can settle an auction (false since v3 — settlement is restricted to the
keeper, the seller, or the winner) and that *every* amount you escrow is
refundable (false for winning bids and accepted offers, which pay the seller).

Nothing in the repo linked to this file, so the accurate copy was the invisible
one while the inaccurate copy shipped. Keeping two copies of a user-facing rules
document is how that happens, so there is now one.

## Editing the rules

Edit `app/src/pages/docs/capabilities.md`. When you change a rule there, check
whether it also needs to change in:

- `contracts/src/*.sol` NatSpec — the rules as enforced on chain
- `docs/IMMUTABILITY_TRANSITION.md` — role and upgrade lifecycle
- `contracts/AUDIT_REPORT.md` — the architecture description sent to auditors
- `app/src/pages/docs/faq.md` — the user-facing FAQ

The on-chain contracts are the ultimate source of truth. Where a document and
the contracts disagree, the contracts are right and the document is a bug.
