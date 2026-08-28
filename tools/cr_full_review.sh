#!/usr/bin/env bash
# Full-codebase CodeRabbit CLI review, per-directory (free-tier file-count workaround).
# Diffs every directory against the repo's initial commit so all files count as changed.
# Same pattern as the 2026-08-23 full review (base b829cc8). Output: one appended report.
set -u
BASE=b829cc886db4a0607a0faf4c43cf929be756d1ba
OUT="$HOME/coderabbit-full-review-v3.md"
DIRS=(
  contracts/src
  contracts/test
  contracts/script
  backend/internal/api
  backend/internal/db
  backend/internal/indexer
  backend/internal/keeper
  backend/internal/auth
  backend/internal/chain
  backend/internal/verifier
  backend/internal/graphql
  backend/internal/connectrpc
  backend/internal/ws
  backend/internal/sse
  backend/internal/media
  backend/internal/imagestore
  backend/internal/webhook
  backend/internal/ratelimit
  backend/internal/rpcpool
  backend/internal/cache
  backend/internal/config
  backend/internal/crypto
  backend/internal/dataloader
  backend/internal/health
  backend/internal/nonce
  backend/cmd
  app/src
  frontend
  tools
  .github
)
echo "# CodeRabbit full-codebase review (v3 pass) — started $(date)" > "$OUT"
echo "Base commit: $BASE  HEAD: $(git rev-parse --short HEAD)" >> "$OUT"
for d in "${DIRS[@]}"; do
  [ -d "$d" ] || { echo "skip missing $d" >> "$OUT"; continue; }
  echo "" >> "$OUT"
  echo "## ===== $d =====" >> "$OUT"
  coderabbit review --plain --type committed --base-commit "$BASE" --dir "$d" >> "$OUT" 2>&1
  echo "[done $d $(date +%H:%M)]" >> "$OUT"
  sleep 5
done
echo "" >> "$OUT"
echo "# ALL DIRECTORIES COMPLETE $(date)" >> "$OUT"
