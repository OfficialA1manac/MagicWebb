# Deploying MagicWebb on Kubernetes (optional path)

**Fly.io is the active production path** (`docs/DEPLOY_FLY.md`, CI auto-deploy).
This directory exists so the same Docker image can run on any Kubernetes
cluster without changes — a portability escape hatch, not a migration
mandate. Use it when traffic, compliance, or multi-region needs outgrow Fly.

## Layout

```text
k8s/
  base/                 chain-agnostic Deployment + Service + common config
  overlays/coston2/     live trading network (contracts + indexer + keepers)
  overlays/songbird/    read-only until contracts deploy (audit + multisig gate)
  overlays/flare/       read-only until contracts deploy (audit + multisig gate)
```

One release per chain — separate namespace, separate Neon database, separate
Redis, separate origin. Never point two overlays at the same database.

## Deploy one network

```bash
# 0. Define once — used by the secret AND the Docker build arg below:
export WC_PROJECT_ID='your_reown_project_id'

# 1. Create the namespace + per-network secrets (never committed):
kubectl create namespace magicwebb-coston2
kubectl -n magicwebb-coston2 create secret generic mw-secrets \
  --from-literal=POSTGRES_URL='postgres://…neon…' \
  --from-literal=JWT_SECRET='…' \
  --from-literal=KEEPER_KEY='…' \
  --from-literal=WC_PROJECT_ID="$WC_PROJECT_ID" \
  --from-literal=REDIS_URL='rediss://…'   # optional; omit for in-memory

# 2. Build/push the image (same Dockerfile as Fly). Define the tag ONCE so
#    build, push and the overlay all reference the same image:
IMAGE_TAG="$(git rev-parse --short HEAD)"
docker build -t ghcr.io/officiala1manac/magicwebb:"$IMAGE_TAG" \
  --build-arg REOWN_PROJECT_ID="$WC_PROJECT_ID" .
docker push ghcr.io/officiala1manac/magicwebb:"$IMAGE_TAG"

# 3. Point the overlay at the tag and apply:
cd k8s/overlays/coston2
kustomize edit set image ghcr.io/officiala1manac/magicwebb:"$IMAGE_TAG"
kubectl apply -k .
```

Health: `/healthz` is liveness only (no DB/RPC — a slow chain never restarts
the pod); `/readyz` gates traffic on DB + RPC + head lag.

## Scaling beyond one replica

The default is `replicas: 1` per chain. Before scaling up:

1. Set `GRPC_PORT` and `GRPC_PEERS` (or add a headless Service) so the
   gRPC event mesh fans WebSocket events across pods and keeper leader
   election picks a single settler.
2. Set `REDIS_URL` to a shared Redis so the read caches agree across pods
   (rate-limit counters and SIWE nonces are Postgres-backed and already
   shared — see `ARCHITECTURE.md`).

Without both, a second replica double-settles nothing (election defaults
safe) but WS clients on pod B miss events from pod A.

## Enabling Songbird / Flare trading

Same procedure as Fly (`deployments/README.md`): run the forge deploy script
and record the addresses in `deployments/<network>.json` first — that file is
the single source of truth for contract addresses. Then copy those values into
the overlay's `*_ADDR` literals verbatim (read them back with
`jq .contracts deployments/<network>.json`; never hand-type addresses into the
overlay that the JSON does not carry) and redeploy. Divergence matters: the
env literals are what the server actually indexes, so an overlay that drifts
from the JSON silently runs a different contract set than every other
consumer of `deployments/<network>.json` — diff the two before `kubectl
apply`. Until then the overlay
boots read-only: browse/search/profiles work, trading CTAs point to Coston2.
