#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# tools/check-fly-sync.sh
#
# Verifies that every deployed network is serving the EXACT git SHA that's on
# origin/main. The /healthz endpoint on Fly returns an X-MW-Build-SHA header
# whose value is injected into the Go binary at link time via -ldflags
# '-X .../api.MWServerBuildSHA=<sha>'. The Makefile's `build` target drives
# this from `git rev-parse HEAD` so every committed binary advertises its
# provenance.
#
# Targets:
#   Default — EVERY network in deployments/*.json whose status is not
#   "not-deployed". Checking only one network is how a silent drift survives:
#   a green tick for Coston2 says nothing about Songbird or Flare, and the
#   drift this tool exists to catch has historically hit exactly one network
#   at a time (a cancelled/timed-out matrix leg leaves one origin behind while
#   the run still looks green).
#
#   LIVE_URL=<origin> — check that ONE origin instead. deploy.yml uses this to
#   gate each matrix leg against the network it just deployed.
#
# Contract:
#   live X-MW-Build-SHA MUST equal `git rev-parse origin/main`. Any mismatch
#   exits 1 with a one-line actionable diff. CI invokes this from
#   .github/workflows/deploy.yml AFTER `fly deploy` reports success — so a
#   layer-cache-stale deploy is a loud failure in the Actions UI rather than
#   a silent drift observed on the live URL hours later.
#
# Exit codes:
#    0 — in sync (every checked origin matches origin/main)
#    1 — out of sync (at least one origin serves a different commit)
#    2 — environment error (no origin/main, no curl, no origin reachable, etc)
# ─────────────────────────────────────────────────────────────────────────────
set -u

ORIGIN_SHA="$(git rev-parse origin/main 2>/dev/null)"

bold()   { printf '\033[1m%s\033[0m\n' "$*"; }
red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }

if [ -z "$ORIGIN_SHA" ]; then
  red "FAIL: cannot read origin/main (no git remote or missing fetch)"
  exit 2
fi

# check_one <origin-url> → 0 in sync, 1 drift, 2 environment error.
check_one() {
  local live_url="$1"
  echo "  live URL     : ${live_url}"

  local headers_tmp http live_sha
  headers_tmp="$(mktemp)"

  # Capture status + the X-MW-Build-SHA header in one shot.
  # curl -D- dumps response headers while -o sends the body to /dev/null.
  http="$(curl -sS -D "${headers_tmp}" -o /dev/null \
    --max-time 10 \
    "${live_url}/healthz" \
    -w '%{http_code}' 2>&1)" || {
      red "  FAIL: curl errored ($http)"
      rm -f "$headers_tmp"
      return 2
    }

  case "$http" in
    200) ;;
    000) red "  FAIL: no response (DNS / TLS / network)"; rm -f "$headers_tmp"; return 2 ;;
    *)   red "  FAIL: /healthz returned HTTP $http"; tail -n 5 "$headers_tmp" || true
         rm -f "$headers_tmp"; return 2 ;;
  esac

  live_sha="$(grep -i '^x-mw-build-sha:' "${headers_tmp}" \
    | tr -d '\r' \
    | awk '{print $2}' \
    | tr -d '[:space:]')"
  rm -f "$headers_tmp"

  if [ -z "$live_sha" ]; then
    red "  FAIL: /healthz responded 200 but no X-MW-Build-SHA header"
    yellow "       (binary was built WITHOUT Makefile -ldflags injection)"
    yellow "       rebuild with 'make build' (NOT 'go build') and redeploy"
    return 2
  fi

  # Tolerate short-SHA (7+ chars) — some callers shorten. Strict equality
  # wins when both sides are 40-char.
  if [ "${#live_sha}" -lt 7 ] || [ "${#live_sha}" -gt 40 ]; then
    red "  FAIL: malformed X-MW-Build-SHA: '${live_sha}' (wrong length)"
    return 2
  fi

  if [ "${live_sha}" = "${ORIGIN_SHA}" ]; then
    green "  ✅   ${live_sha}  ==  ${ORIGIN_SHA}"
    green "  Serving origin/main — perfect sync."
    return 0
  fi

  # Suffix-compare tolerates the common case where one side is short.
  # Verify the shorter SHA is an actual prefix of the longer one rather than
  # just matching first-7 blindly (which could let a malformed SHA pass the
  # gate by coincidence).
  if [ "${#live_sha}" -lt "${#ORIGIN_SHA}" ]; then
    if [ "${live_sha}" = "${ORIGIN_SHA:0:${#live_sha}}" ]; then
      green "  ✅   ${live_sha}  ≺  ${ORIGIN_SHA} (live is prefix of origin)"
      bold  "  Note: Fly served a ${#live_sha}-char SHA, origin is ${#ORIGIN_SHA}-char — same commit."
      return 0
    fi
  elif [ "${#ORIGIN_SHA}" -lt "${#live_sha}" ]; then
    if [ "${ORIGIN_SHA}" = "${live_sha:0:${#ORIGIN_SHA}}" ]; then
      green "  ✅   ${ORIGIN_SHA}  ≺  ${live_sha} (origin is prefix of live)"
      bold  "  Note: origin is ${#ORIGIN_SHA}-char SHA, live served ${#live_sha}-char — same commit."
      return 0
    fi
  fi
  # Equal lengths already failed the strict-equality check above, so the SHAs
  # are simply different commits — a first-7-chars match must NOT pass the
  # gate (two distinct 40-char SHAs sharing a 7-char prefix would slip
  # through). Fall through to DRIFT.

  red "  ❌   DRIFT detected"
  red "       live      : ${live_sha}"
  red "       origin    : ${ORIGIN_SHA}"
  yellow "  Most likely cause: a Docker layer cache pinned the previous binary's"
  yellow "  static assets despite Fly recording a new release, or this network's"
  yellow "  deploy leg was cancelled/timed out while the run still went green."
  yellow "  Re-run:  fly deploy --remote-only --no-cache   (with this app selected)"
  return 1
}

# Build the target list. `tr -d '\r'` is load-bearing on Windows/Git Bash:
# jq emits CRLF there, and a trailing \r reaches curl as part of the URL —
# "curl: (3) URL rejected: Malformed input to a URL function" — which fails
# every entry except the last one on the line-split.
TARGETS=()
if [ -n "${LIVE_URL:-}" ]; then
  TARGETS+=("$(printf '%s' "$LIVE_URL" | tr -d '\r')")
else
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
  if ! command -v jq >/dev/null 2>&1; then
    red "FAIL: jq is required to enumerate deployments/*.json"
    yellow "      (or set LIVE_URL=<origin> to check a single network)"
    exit 2
  fi
  while IFS= read -r url; do
    [ -n "$url" ] && TARGETS+=("$url")
  done < <(jq -r 'select(.status!="not-deployed")|.app.origin//empty' \
             "$repo_root"/deployments/*.json 2>/dev/null | tr -d '\r')
fi

if [ "${#TARGETS[@]}" -eq 0 ]; then
  red "FAIL: no deployed networks found in deployments/*.json"
  exit 2
fi

bold "════════════════════════════════════════════════════════════════════════════"
bold "  Fly ↔ origin/main sync gate"
bold "════════════════════════════════════════════════════════════════════════════"
echo
bold "  origin/main  : ${ORIGIN_SHA}"
bold "  networks     : ${#TARGETS[@]}"
echo

worst=0
declare -a SUMMARY=()
for url in "${TARGETS[@]}"; do
  check_one "$url"
  rc=$?
  case "$rc" in
    0) SUMMARY+=("  ✅  ${url}") ;;
    1) SUMMARY+=("  ❌  ${url}  (DRIFT)") ;;
    *) SUMMARY+=("  ⚠   ${url}  (unreachable / no header)") ;;
  esac
  # A drift (1) outranks an environment error (2): report the worst REAL
  # problem, but never let an unreachable origin be mistaken for success.
  if [ "$rc" -ne 0 ]; then
    if [ "$worst" -eq 0 ] || { [ "$rc" -eq 1 ] && [ "$worst" -eq 2 ]; }; then
      worst="$rc"
    fi
  fi
  echo
done

bold "────────────────────────────────────────────────────────────────────────────"
bold "  Summary"
for line in "${SUMMARY[@]}"; do echo "$line"; done
echo
if [ "$worst" -eq 0 ]; then
  green "  All ${#TARGETS[@]} network(s) serving origin/main."
else
  red "  At least one network is NOT serving origin/main."
fi
exit "$worst"
