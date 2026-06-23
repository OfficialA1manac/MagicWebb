#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# tools/check-fly-sync.sh
#
# Verifies the live magicwebb.fly.dev is serving the EXACT git SHA that's on
# origin/main. The /healthz endpoint on Fly returns an X-MW-Build-SHA header
# whose value is injected into the Go binary at link time via -ldflags
# '-X .../api.MWServerBuildSHA=<sha>'. The Makefile's `build` target drives
# this from `git rev-parse HEAD` so every committed binary advertises its
# provenance.
#
# Contract:
#   live X-MW-Build-SHA MUST equal `git rev-parse origin/main`. Any mismatch
#   exits 1 with a one-line actionable diff. CI invokes this from
#   .github/workflows/deploy.yml AFTER `fly deploy` reports success — so a
#   layer-cache-stale deploy is a loud failure in the Actions UI rather than
#   a silent drift observed on the live URL hours later.
#
# Exit codes:
#    0 — in sync (live header == origin/main SHA)
#    1 — out of sync (live header != origin/main SHA)
#    2 — environment error (no origin/main, no curl, no live reachable, etc)
# ─────────────────────────────────────────────────────────────────────────────
set -u

ORIGIN_SHA="$(git rev-parse origin/main 2>/dev/null)"
LIVE_URL="${LIVE_URL:-https://magicwebb.fly.dev}"

bold()   { printf '\033[1m%s\033[0m\n' "$*"; }
red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }

if [ -z "$ORIGIN_SHA" ]; then
  red "FAIL: cannot read origin/main (no git remote or missing fetch)"
  exit 2
fi

bold "════════════════════════════════════════════════════════════════════════════"
bold "  Fly ↔ origin/main sync gate"
bold "════════════════════════════════════════════════════════════════════════════"
echo

bold "  origin/main  : ${ORIGIN_SHA}"
echo "  live URL     : ${LIVE_URL}"
echo

# Capture both status + body + the X-MW-Build-SHA header in one shot.
# curl -D- dumps response headers to stdout while -o sends body to /tmp.
HEADERS_TMP="$(mktemp)"
trap 'rm -f "$HEADERS_TMP"' EXIT

HTTP="$(curl -sS -D "${HEADERS_TMP}" -o /dev/null \
  --max-time 10 \
  "${LIVE_URL}/healthz" \
  -w '%{http_code}' 2>&1)" \
  || { red "FAIL: curl errored ($HTTP)"; exit 2; }

case "$HTTP" in
  200) ;;
  000) red "FAIL: no response (DNS / TLS / network)"; exit 2 ;;
  *)   red "FAIL: /healthz returned HTTP $HTTP"; tail -n 5 "$HEADERS_TMP" || true; exit 2 ;;
esac

LIVE_SHA="$(grep -i '^x-mw-build-sha:' "${HEADERS_TMP}" \
  | tr -d '\r' \
  | awk '{print $2}' \
  | tr -d '[:space:]')"

if [ -z "$LIVE_SHA" ]; then
  red "FAIL: /healthz responded 200 but no X-MW-Build-SHA header"
  yellow "       (binary was built WITHOUT Makefile -ldflags injection)"
  yellow "       rebuild with 'make build' (NOT 'go build') and redeploy"
  exit 2
fi

# Tolerate short-SHA (7+ chars) — some callers shorten. Strict equality
# wins when both sides are 40-char.
if [ "${#LIVE_SHA}" -lt 7 ] || [ "${#LIVE_SHA}" -gt 40 ]; then
  red "FAIL: malformed X-MW-Build-SHA: '${LIVE_SHA}' (wrong length)"
  exit 2
fi

if [ "${LIVE_SHA}" = "${ORIGIN_SHA}" ]; then
  green  "  ✅   ${LIVE_SHA}  ==  ${ORIGIN_SHA}"
  green  "  Fly is serving origin/main — perfect sync."
  exit 0
fi

# Suffix-compare tolerates the common case where one side is short.
LIVE_SHORT="${LIVE_SHA:0:7}"
ORIG_SHORT="${ORIGIN_SHA:0:7}"
if [ "${LIVE_SHORT}" = "${ORIG_SHORT}" ]; then
  green  "  ✅   ${LIVE_SHORT}  ==  ${ORIG_SHORT} (short-match; lengths differ)"
  bold   "  Note: Fly served a ${#LIVE_SHA}-char SHA, origin is ${#ORIGIN_SHA}-char — both refer to commit ${ORIG_SHORT}."
  exit 0
fi

red "  ❌   DRIFT detected"
red       "       live      : ${LIVE_SHA}"
red       "       origin    : ${ORIGIN_SHA}"
echo
yellow "Most likely cause: v23 Docker layer cache pinned the previous"
yellow "binary's static assets despite Fly recording a new release. Re-run:"
yellow "    fly deploy --remote-only --no-cache"
echo
exit 1
