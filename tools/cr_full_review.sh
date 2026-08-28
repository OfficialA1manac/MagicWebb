#!/usr/bin/env bash
# Full-codebase CodeRabbit CLI review, per-directory (free-tier workaround).
# Diffs each directory against the repo's initial commit so all files count as
# changed. Free tier allows 3 reviews per ~1h window: on "Review limit reached"
# the runner sleeps 51 min and retries the SAME directory until it succeeds.
# Output: appended report at ~/coderabbit-full-review-v3.md
set -u
BASE=b829cc886db4a0607a0faf4c43cf929be756d1ba
OUT="$HOME/coderabbit-full-review-v3.md"
TMP=$(mktemp)
DIRS=(
  contracts/script
  backend/internal/db
  backend/internal/api
  backend/internal/graphql
  backend/internal/connectrpc
  backend/internal/indexer
  backend/internal/imagestore
  backend/internal/keeper
  backend/internal/auth
  backend/internal/chain
  backend/internal/verifier
  backend/internal/ws
  backend/internal/sse
  backend/internal/media
  backend/internal/webhook
  backend/internal/ratelimit
  backend/internal/rpcpool
  backend/internal/cache
  backend/internal/config
  backend/internal/crypto
  backend/internal/dataloader
  backend/internal/health
  backend/cmd
  app/src
  tools
  .github
  docs
)
echo "" >> "$OUT"
echo "# Resumed limit-aware sweep $(date) — contracts/src + contracts/test + nonce already done" >> "$OUT"
for d in "${DIRS[@]}"; do
  [ -d "$d" ] || { echo "skip missing $d" >> "$OUT"; continue; }
  tries=0
  while : ; do
    coderabbit review --plain --type committed --base-commit "$BASE" --dir "$d" > "$TMP" 2>&1
    if grep -q "Review limit reached" "$TMP"; then
      tries=$((tries+1))
      if [ "$tries" -gt 15 ]; then
        { echo ""; echo "## ===== $d ====="; echo "GAVE UP after 15 limit waits"; } >> "$OUT"
        break
      fi
      sleep 3060  # 51 min — limit window resets in <=48
    else
      { echo ""; echo "## ===== $d ====="; cat "$TMP"; echo "[done $d $(date +%H:%M)]"; } >> "$OUT"
      sleep 10
      break
    fi
  done
done
echo "" >> "$OUT"
echo "# SWEEP COMPLETE $(date)" >> "$OUT"
rm -f "$TMP"
