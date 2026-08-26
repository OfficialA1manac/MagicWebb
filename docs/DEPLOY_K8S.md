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

# 2. Build/push the image (same Dockerfile as Fly):
docker build -t ghcr.io/officiala1manac/magicwebb:$(git rev-parse --short HEAD) \
  --build-arg REOWN_PROJECT_ID="$WC_PROJECT_ID" .
docker push ghcr.io/officiala1manac/magicwebb:$(git rev-parse --short HEAD)

# 3. Point the overlay at the tag and apply:
cd k8s/overlays/coston2
kustomize edit set image ghcr.io/officiala1manac/magicwebb:<tag>
kubectl apply -k .
```

Health: `/healthz` is liveness only (no DB/RPC — a slow chain never restarts
the pod); `/readyz` gates traffic on DB + RPC + head lag.

## Scaling beyond one replica

The default is `replicas: 1` per chain. Before scaling up:

1. Set `GRPC_PORT` and `GRPC_PEERS` (or add a headless Service) so the
   gRPC event mesh fans WebSocket events across pods and keeper leader
   election picks a single settler.
2. Set `REDIS_URL` to a shared Redis so rate limits and cache agree.

Without both, a second replica double-settles nothing (election defaults
safe) but WS clients on pod B miss events from pod A.

## Enabling Songbird / Flare trading

Same procedure as Fly (`deployments/README.md`): run the forge deploy script,
fill the `*_ADDR` literals in the overlay's `kustomization.yaml`, mirror the
addresses into `deployments/<network>.json`, redeploy. Until then the overlay
boots read-only: browse/search/profiles work, trading CTAs point to Coston2.
