# IMG-3: S3-Compatible Image Store Backend

**Status:** ✅ Implemented | **Commit:** `3dde467` | **Category:** Storage / Infrastructure

---

## Overview

IMG-3 adds an S3-compatible storage backend to the MagicWebb image store, replacing
Postgres BYTEA blob storage with object storage (AWS S3, MinIO, Cloudflare R2,
Backblaze B2, DigitalOcean Spaces, or any S3-compatible service).

When enabled, **blob bodies** are stored in S3 while **metadata** (hashes, MIME
types, refcounts, quota counters) remains in the `nft_image_blobs` Postgres table.
This is a drop-in replacement — all callers (indexer, API handlers, GraphQL
resolvers) use the existing `imagestore.Store` interface and require zero code
changes beyond configuration.

### Why S3?

| Concern | Postgres BYTEA | S3 |
|---------|---------------|-----|
| Storage cost | Expensive (PG storage) | Cheap ($0.023/GB for S3, ~free for R2) |
| Connection drain | Large blob transfers consume PG connections | No PG connection used for blob I/O |
| Bandwidth | Served from PG | Served from object storage (optionally CDN) |
| Max blob size | 1 GB (PG bytea limit) | 5 TB (S3 limit) |
| CDN integration | Requires custom caching layer | S3 → CloudFront / R2 built-in |

### Benefits

- **Frees Postgres storage**: BYTEA rows for images (which can be up to 8 MiB each) no longer consume PG disk space
- **Frees Postgres connections**: Image GET requests don't hold PG connections for blob transfer
- **CDN-ready**: S3 objects can be fronted by CloudFront, Cloudflare, or any CDN for edge caching
- **Multi-cloud**: Works with AWS S3, MinIO (self-hosted), Cloudflare R2, Backblaze B2, DigitalOcean Spaces
- **Pluggable**: Swapping backends requires only environment variable changes — zero code changes

---

## Architecture

### Storage Model

```
                    ┌─────────────────────┐
                    │   nft_image_blobs   │  ← Postgres (metadata only)
                    │  sha256 (PK)        │
                    │  mime               │
                    │  byte_length        │
                    │  source_uri         │
                    │  collection         │
                    │  refcount           │
                    │  parent_hash        │
                    │  thumb_width        │
                    │  body = ''::bytea   │  ← EMPTY when S3 backend
                    └─────────┬───────────┘
                              │ sha256 → S3 key
                              ▼
                    ┌─────────────────────┐
                    │   S3 Bucket         │  ← Object storage (blob bodies)
                    │                     │
                    │  blobs/<sha256>     │  ← content-addressed key
                    │  blobs/<sha256>     │
                    │  ...                │
                    └─────────────────────┘
```

### Key Design Decisions

1. **Content-addressed keys**: S3 object keys are `blobs/<sha256hex>`. The SHA-256 hash is computed from the blob bytes during ingest, guaranteeing deduplication at the storage layer (identical bytes = identical key).

2. **Metadata in Postgres**: All queryable fields (MIME, source URI, byte length, collection, refcount, parent hash, thumbnail width) stay in `nft_image_blobs`. This means SQL queries (quota enforcement, thumbnail lookup, existence checks) work identically regardless of backend.

3. **Dual-write ordering**: On `PutImage`, the S3 upload happens FIRST. If it succeeds, the Postgres metadata is inserted. If it fails, Postgres is never touched. This means orphaned S3 objects (S3 success + PG failure) are possible but harmless — they're unreferenced, content-addressed blobs. The reverse (PG row pointing to missing S3 object) would cause 500 errors on GET, so S3-first is the safer ordering.

4. **Interface-based**: The `imagestore.Store` interface abstracts all storage operations. `S3Store` implements this interface, making it a drop-in replacement for the default `db.Q` (Postgres BYTEA) implementation.

### Store Interface

```go
type Store interface {
    PutImage(ctx, sha256hex, mime, collection, sourceURI string, body []byte) error
    PutThumbnail(ctx, sha256hex, mime, parentHash, collection, sourceURI string, body []byte, width int) error
    GetImageByParent(ctx, parentHash string, width int, preferWebP bool) (Blob, error)
    GetImage(ctx, sha256hex string) (Blob, error)
    HasImage(ctx, sha256hex string) (bool, error)
    TotalBlobBytes(ctx) (int64, error)
    CountBlobsForCollection(ctx, collection string) (int, error)
}
```

### Internal Interfaces

To enable unit testing without live S3 or Postgres connections, `S3Store` uses two internal interfaces:

- **`dbExecutor`**: Abstracts `Exec()` and `QueryRow()` from `*pgxpool.Pool`. Satisfied by both the real pool and `pgxmock.PgxPoolIface`.
- **`s3Client`**: Abstracts `PutObject()` and `GetObject()` from `*minio.Client`. Satisfied by the real client (via `realS3Client` adapter) and a mock implementation in tests.

---

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `IMG_STORE_BACKEND` | No | `""` | Set to `"s3"` to enable S3 backend. Empty = Postgres BYTEA (default). |
| `S3_ENDPOINT` | When `IMG_STORE_BACKEND=s3` | — | S3-compatible endpoint (e.g. `s3.amazonaws.com`, `play.min.io:9000`) |
| `S3_BUCKET` | When `IMG_STORE_BACKEND=s3` | — | Bucket name for blob storage |
| `S3_ACCESS_KEY` | When `IMG_STORE_BACKEND=s3` | — | S3 access key ID |
| `S3_SECRET_KEY` | When `IMG_STORE_BACKEND=s3` | — | S3 secret access key |
| `S3_USE_SSL` | No | `false` | Set to `"true"` for HTTPS connections |

### Provider Examples

#### AWS S3
```env
IMG_STORE_BACKEND=s3
S3_ENDPOINT=s3.us-east-1.amazonaws.com
S3_BUCKET=magicwebb-nft-images
S3_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
S3_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
S3_USE_SSL=true
```

#### Cloudflare R2
```env
IMG_STORE_BACKEND=s3
S3_ENDPOINT=<account-id>.r2.cloudflarestorage.com
S3_BUCKET=magicwebb-images
S3_ACCESS_KEY=<r2-access-key-id>
S3_SECRET_KEY=<r2-secret-access-key>
S3_USE_SSL=true
```

#### MinIO (local development)
```env
IMG_STORE_BACKEND=s3
S3_ENDPOINT=localhost:9000
S3_BUCKET=magicwebb-images
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin
S3_USE_SSL=false
```

#### Backblaze B2
```env
IMG_STORE_BACKEND=s3
S3_ENDPOINT=s3.us-west-004.backblazeb2.com
S3_BUCKET=magicwebb-images
S3_ACCESS_KEY=<b2-application-key-id>
S3_SECRET_KEY=<b2-application-key>
S3_USE_SSL=true
```

### Startup Validation

At startup, `NewS3Store()` performs a **bucket existence check** via `client.BucketExists()`.
If the bucket doesn't exist or is unreachable, the server **fatal-exits** with a clear error
message. This prevents silent fallback to Postgres when S3 is misconfigured.

---

## How It Works

### Image Ingest Flow

```
NFT metadata fetch → Image download → Zig SHA-256 hash → MIME sniff
                                                           ↓
                                              ┌──────────────────────┐
                                              │ S3: PutObject(blobs/<hash>, body, mime)
                                              │ PG: INSERT nft_image_blobs(body=''::bytea)
                                              └──────────────────────┘
                                                           ↓
                                              Thumbnail generation (128/256/512px)
                                                           ↓
                                              ┌──────────────────────┐
                                              │ S3: PutObject(blobs/<thumb-hash>, thumb)
                                              │ PG: INSERT nft_image_blobs(parent_hash=...)
                                              └──────────────────────┘
```

### Image Serve Flow

```
GET /api/v1/img/<sha256>
         ↓
    ┌────────────┐
    │ PG metadata │ → SELECT mime, source_uri FROM nft_image_blobs WHERE sha256=$1
    └─────┬──────┘
          ↓
    ┌────────────┐
    │ S3 download │ → GET /bucket/blobs/<sha256>
    └─────┬──────┘
          ↓
    Response: body + Content-Type + Cache-Control: immutable
```

### Thumbnail Serve Flow

```
GET /api/v1/img/<parent-hash>?size=256
         ↓
    ┌────────────┐
    │ PG lookup   │ → SELECT sha256, mime FROM nft_image_blobs
    │             │    WHERE parent_hash=$1 AND thumb_width=256
    └─────┬──────┘
          ↓
    ┌────────────┐
    │ S3 download │ → GET /bucket/blobs/<thumb-hash>
    └─────┬──────┘
          ↓
    Response: thumbnail body — JPEG or WebP (per Accept header)
```

---

## Migration Between Backends

### Postgres → S3

1. Set up your S3 bucket with the appropriate credentials.
2. Update your `.env` / deployment config with the S3 variables.
3. Deploy. The server will:
   - Validate the S3 bucket exists
   - Start writing new blobs to S3
   - Read existing blobs from Postgres (dual-read compatibility)

**Note**: New blobs go to S3; existing Postgres blobs remain in Postgres and are still served correctly. No migration of existing blobs is needed — the `Store` interface routes reads to the correct backend.

### S3 → Postgres (rollback)

1. Remove the S3 env vars (or set `IMG_STORE_BACKEND=""`).
2. Deploy. The server will revert to Postgres BYTEA for all new blobs.

**Note**: Existing S3 blobs will no longer be accessible (their `body` column is `''::bytea`). If you need to migrate back permanently, you must re-upload all S3 blobs to Postgres.

---

## Quotas & Limits

All quota enforcement remains in Postgres and is **backend-agnostic**:

| Limit | Value | Where |
|-------|-------|-------|
| Max blob size | 8 MiB | `imagestore.MaxBlobBytes` |
| Max total bytes | 256 MiB | `imagestore.MaxTotalBlobBytes` |
| Max blobs per collection | 1,000 | `imagestore.MaxBlobCountPerCollection` |
| S3 download timeout | 30 seconds | `S3Store.download()` |

---

## Testing

### Unit Tests

The S3Store has comprehensive unit tests in `s3_store_test.go` (45 test cases):

```bash
cd backend
go test ./internal/imagestore/... -count=1 -short -v
```

Tests cover:
- All 7 `Store` interface methods (PutImage, PutThumbnail, GetImage, GetImageByParent, HasImage, TotalBlobBytes, CountBlobsForCollection)
- S3 upload errors
- S3 download errors
- Postgres metadata errors
- `pgx.ErrNoRows` sentinel handling
- Round-trip (Put → Get returns same bytes)
- Context-aware reader timeout enforcement

### Mock Architecture

Tests use a **mock S3 client** (`mockS3Client`) with an in-memory map instead of a real S3 endpoint, and **pgxmock** for Postgres. This avoids:

- Network dependencies (no real S3/MinIO needed)
- Postgres dependencies (no real database needed)
- Complex HTTP test servers (no minio-go HTTP path resolution issues)

---

## Dependencies

- `github.com/minio/minio-go/v7` — S3-compatible client library
- `github.com/jackc/pgx/v5` — Postgres driver (metadata only)

---

## Files

| File | Purpose |
|------|---------|
| `backend/internal/imagestore/s3_store.go` | S3Store implementation (Store interface + S3 client + context-aware download) |
| `backend/internal/imagestore/s3_store_test.go` | 45 unit tests (mock S3 + pgxmock PG) |
| `backend/internal/imagestore/imagestore.go` | Store interface definition + shared Put/Get/Has helpers |
| `backend/internal/config/config.go` | S3 configuration fields + env var loading |
| `backend/cmd/server/main.go` | S3 initialization + wiring to Runner and API |
| `backend/internal/indexer/runner.go` | WithImgStore() option + imgStore field |
| `backend/internal/indexer/handlers.go` | imgStore passthrough |
| `backend/internal/indexer/metadata.go` | Uses imgStore for all blob I/O |
| `backend/internal/api/media.go` | store() helper + handleProxy/handleRetry/handleImageByHash |
| `backend/internal/api/rest.go` | Mount() accepts imgStore, wires to MediaService |

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| Server fails to start with `FATAL: IMG_STORE_BACKEND=s3 requires S3_ENDPOINT and S3_BUCKET` | Missing required env vars | Set `S3_ENDPOINT` and `S3_BUCKET` |
| Server fails to start with `s3store: bucket check failed` | Wrong credentials, network issue, or bucket doesn't exist | Verify credentials, endpoint, and that the bucket exists |
| 500 errors on image GET: `s3store: download <hash>: ...` | S3 object missing or S3 connectivity issue | Check S3 bucket for the object, verify network access |
| Images served slowly | S3 latency or no CDN | Consider adding a CDN (CloudFront / Cloudflare) in front of S3 |
| `s3store: put object blobs/<hash>: The specified bucket does not exist` | Bucket was deleted or renamed after startup | Recreate the bucket or update S3_BUCKET env var |
