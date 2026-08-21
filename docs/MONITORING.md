# Monitoring & post-launch operations

Every network app exposes the same surface.

| Endpoint | What |
|---|---|
| `GET /healthz` | liveness (200 = process up) |
| `GET /readyz` | readiness: DB reachable |
| `GET /api/v1/indexer/slo` | Prometheus `head_lag_blocks` gauge (indexer vs chain head) |
| `GET /internal/metrics` | full Prometheus scrape: HTTP, RPC pool failovers, WS clients, keeper gas, cache hit-rate |
| `GET /metrics` | human HTML dashboard of the same numbers |
| `GET /metrics/gas` | keeper gas spend over time |

Alerts the binary sends itself (all optional env):
- `DISCORD_WEBHOOK_URL` / `PROMETHEUS_WEBHOOK_URL` — keeper balance below
  `GAS_ALERT_THRESHOLD_WEI`, RPC pool exhausted, indexer stalled.
- `SENTRY_DSN` — panics and 5xx with 10 % trace sampling.
- `OTEL_EXPORTER_OTLP_ENDPOINT` — traces.

What to watch per network
- `head_lag_blocks` > 30 for > 5 min → RPC trouble or indexer crash-loop (`fly logs`).
- keeper native balance → top up the `KEEPER_KEY` address; fee sweep needs
  `FEE_SWEEP_MIN_WEI` headroom.
- `ws_clients` suddenly 0 while HTTP traffic continues → WebSocket upgrade broken.
- Neon: endpoint auto-suspend wakes on first connection; sustained 28P01 means the
  endpoint was recreated, not rotated — re-fetch the URI from the Neon API.

Runbooks: `docs/DEPLOY_FLY.md` (redeploy, secrets), `go run ./cmd/chainwipe`
(reset a network's indexed data when contract addresses change), `fly ssh console`
for on-box inspection.
