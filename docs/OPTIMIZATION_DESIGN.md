# Optimization Matrix — Remaining Items Design

Status: 44/50 items complete (88%). 6 remaining + 2 wiring tasks.

---

## GQL-4: GraphQL Subscriptions via WebSocket

**Effort:** 2 weeks | **Priority:** Medium | **Deps:** WS-2, WS-3

### Current State

The GraphQL schema already defines subscription types (`listingUpdated`, `auctionUpdated`,
`activityUpdated`, `notificationUpdated`). The `subscriptionResolver` in `resolver.go`
has working implementations that subscribe to the SSE Broadcaster via `SubscribeRaw()`
and filter events into typed channels. However, the gqlgen transport layer currently
only enables `transport.POST` and `transport.GET` — no WebSocket subscription transport.

### Design

#### Phase 1: Enable graphql-transport-ws (1 day)

Add the `graphql-transport-ws` WebSocket protocol to the gqlgen handler. gqlgen
natively supports `transport.Websocket{}` which implements the
`graphql-transport-ws` sub-protocol (successor to `subscriptions-transport-ws`).

**Changes to `handler.go`:**
```go
// In NewGraphQLServer, add:
srv.AddTransport(transport.Websocket{
    KeepAlivePingInterval: 10 * time.Second,
    Upgrader: websocket.Upgrader{
        CheckOrigin: func(r *http.Request) bool {
            return originAllowed(r) // reuse existing origin check
        },
    },
})
```

#### Phase 2: Wire subscriptions to existing /ws (1 day)

Rather than running a separate WebSocket server for GraphQL, mount the gqlgen
WebSocket handler on the existing Fiber app at `/graphql/ws`. The Fiber WebSocket
upgrade already handles authentication, IP rate limiting, and connection tracking.

**Changes to `handler.go`:**
```go
// In HandleWebSocket, detect graphql-transport-ws sub-protocol:
if c.Get("Sec-WebSocket-Protocol") == "graphql-transport-ws" {
    // Delegate to gqlgen's WebSocket transport
    graphqlServer.ServeWS(c)
    return nil
}
// ... existing WS handler for legacy push events
```

#### Phase 3: RBAC + Filtering (3 days)

Subscriptions must be scoped to the authenticated user:
- `notificationUpdated`: only events for the authenticated wallet address
- `listingUpdated(collection, tokenID)`: public, with optional filters
- `auctionUpdated(auctionID)`: public, with optional filters
- `activityUpdated`: public

JWT authentication is already handled by `HandleWebSocket`. The subscription
resolvers need access to the authenticated address from the Fiber context.
Store it in the gqlgen operation context via a custom middleware:

```go
srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
    addr := auth.FromContext(ctx) // extract from Fiber context
    ctx = context.WithValue(ctx, authCtxKey, addr)
    return next(ctx)
})
```

#### Phase 4: Backpressure + Client Limits (2 days)

GraphQL subscriptions over WS create a per-subscription goroutine + channel.
At 5,000 concurrent WS connections with 3 subscriptions each = 15,000 goroutines.
Add per-connection subscription caps:

```go
const maxSubscriptionsPerConn = 10

func (c *Connection) addSubscription(id string) bool {
    c.subMu.Lock()
    defer c.subMu.Unlock()
    if len(c.activeSubs) >= maxSubscriptionsPerConn {
        return false
    }
    c.activeSubs[id] = struct{}{}
    return true
}
```

#### Phase 5: Testing (1 day)

- Unit tests for subscription resolver filtering
- Integration test for graphql-transport-ws handshake
- Load test: 1,000 concurrent subscription connections

### Files to modify/create:
- `backend/internal/graphql/handler.go` — add WebSocket transport
- `backend/internal/graphql/subscription_transport.go` — NEW: WS upgrade + auth middleware
- `backend/internal/ws/handler.go` — route graphql-ws sub-protocol to gqlgen
- `backend/internal/graphql/resolver.go` — add auth filtering to subscription resolvers

---

## WH-3: Webhooks for Marketplace Events

**Effort:** 1 week | **Priority:** Medium | **Deps:** None

### Current State

`webhook/sender.go` has a complete webhook framework with:
- Retry with exponential backoff (3 attempts: 5s → 30s → 120s)
- HMAC-SHA256 payload signing (`X-Webhook-Signature` header)
- Discord/Slack and Prometheus Alertmanager format support

The framework is currently only used for keeper gas alerts. No marketplace events
trigger webhooks.

### Design

#### Phase 1: Webhook Config Storage (2 days)

Add a `webhook_configs` table and CRUD API so users can register webhook URLs:

```sql
CREATE TABLE webhook_configs (
    id          BIGSERIAL PRIMARY KEY,
    user_addr   TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT,                    -- HMAC secret for signing
    events      TEXT[] NOT NULL DEFAULT '{}', -- ["listing.created", "auction.ended", ...]
    active      BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_addr, url)
);
```

API endpoints (under `/api/v1/admin/webhooks`):
- `POST /` — register a webhook
- `GET /` — list user's webhooks
- `DELETE /:id` — delete a webhook
- `POST /:id/test` — send a test ping

#### Phase 2: Event Types (1 day)

Define marketplace webhook event types matching the SSE event types:

```go
const (
    WebhookListingCreated  = "listing.created"
    WebhookListingUpdated  = "listing.updated"
    WebhookListingSold     = "listing.sold"
    WebhookAuctionCreated  = "auction.created"
    WebhookAuctionBid      = "auction.bid"
    WebhookAuctionEnded    = "auction.ended"
    WebhookOfferCreated    = "offer.created"
    WebhookOfferAccepted   = "offer.accepted"
    WebhookOfferCancelled  = "offer.cancelled"
    WebhookActivity        = "activity"
)
```

#### Phase 3: Event Dispatcher (2 days)

Add a webhook dispatcher that subscribes to the SSE Broadcaster and fans out
events to matching webhook configs:

```go
type Dispatcher struct {
    bcast  *sse.Broadcaster
    q      *db.Q
    sender *webhook.Sender
}

func (d *Dispatcher) Start(ctx context.Context) {
    eventCh, cancel, _ := d.bcast.SubscribeRaw()
    defer cancel()
    for {
        select {
        case <-ctx.Done():
            return
        case ev := <-eventCh:
            d.dispatch(ev)
        }
    }
}

func (d *Dispatcher) dispatch(ev sse.Event) {
    hookEventType := sseToWebhookType(ev.Type)
    configs, _ := d.q.GetWebhookConfigsForEvent(ctx, hookEventType)
    for _, cfg := range configs {
        go d.sender.Send(ctx, cfg.URL, cfg.Secret, ev)
    }
}
```

#### Phase 4: Rate Limiting + Retry (1 day)

Per-webhook rate limiting: max 10 deliveries/second per URL.
Delivery status tracking: log successes/failures to `webhook_delivery_log`.

### Files to modify/create:
- `backend/internal/db/migrations/031_webhooks.sql` — NEW: schema
- `backend/internal/webhook/dispatcher.go` — NEW: event dispatcher
- `backend/internal/webhook/config.go` — NEW: config CRUD
- `backend/internal/api/webhooks.go` — NEW: REST endpoints
- `backend/cmd/server/main.go` — wire dispatcher

---

## IMG-3: S3 Image Backend

**Effort:** 1 week | **Priority:** Low | **Deps:** None

### Current State

Images are stored in Postgres as BYTEA blobs with content-addressed hashing.
The `imagestore.Store` interface abstracts storage (implemented by `*db.Q`).
Quota enforced at 256 MiB total / 1,000 blobs per collection / 8 MiB per blob.

### Design

#### Phase 1: S3 Store Implementation (3 days)

Add an S3-compatible implementation of `imagestore.Store` using the AWS SDK:

```go
type S3Store struct {
    client     *s3.Client
    bucket     string
    prefix     string // "nft-images/"
    db         *db.Q  // metadata still in Postgres
}

func (s *S3Store) PutImage(ctx context.Context, sha256hex, mime, collection, sourceURI string, body []byte) error {
    // Upload to S3
    _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(s.bucket),
        Key:         aws.String(s.prefix + sha256hex),
        Body:        bytes.NewReader(body),
        ContentType: aws.String(mime),
        Metadata: map[string]string{
            "collection": collection,
            "source-uri": sourceURI,
        },
    })
    // Store metadata row in Postgres (sha256, mime, source_uri, byte_length, s3_key)
    return s.db.InsertImageMeta(ctx, sha256hex, mime, collection, sourceURI, len(body))
}

func (s *S3Store) GetImage(ctx context.Context, sha256hex string) (imagestore.Blob, error) {
    // Fetch from S3
    resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(s.bucket),
        Key:    aws.String(s.prefix + sha256hex),
    })
    // ...
}
```

#### Phase 2: Dual Backend + Migration (2 days)

Support both Postgres BYTEA and S3 simultaneously with a `MultiStore`:

```go
type MultiStore struct {
    primary   imagestore.Store // S3 (when configured)
    fallback  imagestore.Store // Postgres BYTEA
}

func (m *MultiStore) GetImage(ctx context.Context, sha256hex string) (imagestore.Blob, error) {
    blob, err := m.primary.GetImage(ctx, sha256hex)
    if err == nil {
        return blob, nil
    }
    return m.fallback.GetImage(ctx, sha256hex)
}
```

Migration: background goroutine copies existing BYTEA blobs to S3, then switches
primary to S3. No downtime — dual-read during migration, write-both after.

#### Phase 3: CDN Integration (2 days, optional)

Add CloudFront or Cloudflare R2 CDN in front of S3 for edge caching.
The `/api/v1/img/<hash>` handler redirects to CDN URL when configured.

### Files to modify/create:
- `backend/internal/imagestore/s3_store.go` — NEW: S3 implementation
- `backend/internal/imagestore/multi_store.go` — NEW: dual-backend wrapper
- `backend/internal/imagestore/imagestore.go` — add S3 config fields
- `backend/internal/config/config.go` — add S3 env vars
- `backend/cmd/server/main.go` — wire S3 store

### Configuration:
```env
IMG_S3_ENDPOINT=https://s3.amazonaws.com       # or R2, MinIO, etc.
IMG_S3_BUCKET=magicwebb-nft-images
IMG_S3_REGION=us-east-1
IMG_S3_ACCESS_KEY=AKIA...
IMG_S3_SECRET_KEY=...
IMG_S3_CDN_URL=https://cdn.magicwebb.xyz      # optional CDN
```

---

## IMG-4: AVIF/WebP Pipeline

**Effort:** 1 week | **Priority:** Low | **Deps:** IMG-3 (optional)

### Current State

The `thumbnail` package supports JPEG, PNG, GIF resizing using Go's `image`
stdlib. WebP and AVIF are detected by the Zig sniffer but passed through
unchanged — no encoding support. The Go stdlib has no WebP or AVIF encoder.

### Design

#### Option A: CGO with libvips (recommended)

Use `libvips` via CGO for high-performance image processing with native
WebP and AVIF encode/decode support. libvips is widely used in production
(imgproxy, sharp) and provides:
- AVIF encode (via libheif or libaom)
- WebP encode (via libwebp)
- JPEG/PNG/GIF encode at 4-10x the throughput of Go's stdlib
- Automatic SIMD acceleration

```go
//go:build vips
package thumbnail

/*
#cgo LDFLAGS: -lvips
#include <vips/vips.h>
*/
import "C"

func GenerateVips(body []byte, mime string, targetWidth int, outputFormat string) ([]byte, string, error) {
    // Initialize libvips
    // Load image from buffer
    // Resize to target width
    // Encode to requested format (webp, avif)
}
```

**Build tags:**
- `vips` — use libvips for all operations
- Default (no tag) — Go stdlib only (JPEG/PNG/GIF)

#### Option B: Pure Go encoders (simpler, slower)

Use pure-Go WebP/AVIF encoders:
- `golang.org/x/image/webp` — decode only (no encode)
- `github.com/kolesa-team/go-webp` — WebP encode
- `github.com/gen2brain/avif` — AVIF encode via CGO wrapper

Pure Go encoders are 3-5x slower than libvips but have zero C dependencies.
Acceptable for low-volume thumbnail generation (<100/min).

#### Phase 1: Format Selection Logic (1 day)

Add content negotiation and format selection based on `Accept` header:

```go
func NegotiateFormat(acceptHeader string, available []string) string {
    // Parse Accept header, return best match
    // "image/avif,image/webp,image/*" → "avif"
    // "image/webp,*/*" → "webp"
    // "*/*" → "jpeg" (default)
}
```

#### Phase 2: Encoding Pipeline (3 days)

Add format-aware generation to the thumbnail pipeline:

```go
func GenerateFormat(body []byte, mime string, targetWidth int, format string) ([]byte, string, error) {
    switch format {
    case "avif":
        return encodeAVIF(body, targetWidth)
    case "webp":
        return encodeWebP(body, targetWidth)
    case "jpeg":
        return Generate(body, "image/jpeg", targetWidth)
    default:
        return Generate(body, mime, targetWidth)
    }
}
```

#### Phase 3: Thumbnail Storage (2 days)

Store generated thumbnails in the imagestore alongside originals:

```
/api/v1/img/<sha256>          → original (full size)
/api/v1/img/<sha256>?size=128 → 128px thumbnail (JPEG/WebP/AVIF per Accept)
/api/v1/img/<sha256>?size=256 → 256px thumbnail
/api/v1/img/<sha256>?size=512 → 512px thumbnail
```

Thumbnails are keyed by `sha256(original_hash + size + format)` so they
share the same content-addressed dedup as originals.

### Files to modify/create:
- `backend/internal/imagestore/thumbnail/negotiate.go` — NEW: format negotiation
- `backend/internal/imagestore/thumbnail/webp.go` — NEW: WebP encoder
- `backend/internal/imagestore/thumbnail/avif.go` — NEW: AVIF encoder  
- `backend/internal/imagestore/thumbnail/thumbnail.go` — add format-aware generation
- `backend/internal/api/media.go` — add `Accept` header parsing
- `Dockerfile` — install libvips package

### Configuration:
```env
IMG_THUMBNAIL_FORMATS=jpeg,webp,avif    # enabled output formats (in preference order)
IMG_THUMBNAIL_QUALITY=80                # default quality (0-100)
IMG_THUMBNAIL_VIPS=false                # use libvips (requires CGO + libvips)
```

---

## SSE-4 Wiring: Proto Oneof → Bridge Integration

**Effort:** 2 days | **Priority:** Low | **Deps:** protoc regeneration

### Current State

SSE-4 defined the proto `oneof` schema in `events.proto` and Go typed event
structs in `event_types.go`. The `events.pb.go` has NOT been regenerated
(the raw descriptor bytes still reflect the old schema without the oneof).
The bridge still uses `json.Marshal(ev.Data)` for all events.

### Design

#### Step 1: protoc Regeneration

Run `protoc` to regenerate `events.pb.go` from the updated `events.proto`:
```bash
cd backend/internal/sse/proto
protoc --go_out=. --go_opt=paths=source_relative events.proto
```

This will produce native Go types for `ListingUpdated`, `AuctionUpdated`, etc.
and add `GetListingUpdated()`, `GetAuctionUpdated()`, etc. accessors to `EventMessage`.

#### Step 2: Bridge Integration

Update `grpc_bridge.go` `Send()` to populate the oneof when the event data
implements `TypedEvent`:

```go
func (b *GrpcEventBridge) Send(ev Event) {
    msg := &proto.EventMessage{
        Origin: b.origin,
        Type:   ev.Type,
        Seq:    ev.Seq,
    }
    
    // SSE-4: populate typed oneof when available
    if te, ok := ev.Data.(TypedEvent); ok {
        switch v := ev.Data.(type) {
        case ListingUpdatedEvent:
            msg.Event = &proto.EventMessage_ListingUpdated{
                ListingUpdated: &proto.ListingUpdated{
                    Collection: v.Collection,
                    TokenId:    v.TokenID,
                    Seller:     v.Seller,
                    PriceWei:   v.PriceWei,
                },
            }
        // ... other typed cases
        }
    } else {
        // Legacy path: JSON-marshal into bytes data
        data, _ := json.Marshal(ev.Data)
        msg.Data = data
    }
    // ...
}
```

#### Step 3: Indexer Integration

Update the indexer to use typed event structs when publishing events:

```go
// Before:
bcast.Publish(sse.Event{Type: "listing-updated", Data: listingRow})

// After:
bcast.Publish(sse.Event{
    Type: "listing-updated",
    Data: sse.ListingUpdatedEvent{
        Collection: listingRow.Collection,
        TokenID:    listingRow.TokenID,
        Seller:     listingRow.Seller,
        PriceWei:   listingRow.PriceWei,
    },
})
```

### Files to modify:
- `backend/internal/sse/proto/events.pb.go` — protoc regeneration
- `backend/internal/sse/grpc_bridge.go` — oneof population
- `backend/internal/indexer/handlers.go` — use typed event structs

---

## ZIG-1 Wiring: Batch Hashing Integration

**Effort:** 2 days | **Priority:** Low | **Deps:** zigmedia build tag

### Current State

`zig_sha256_batch()` and `zig_keccak256_batch()` are implemented in Zig with
Go wrappers. But the batch functions are never called — the image processing
pipeline still calls `hashBytes()` one-at-a-time.

### Design

#### Step 1: Thumbnail Batch Integration

During image ingest, multiple thumbnail sizes are generated from a single
original. Use `hashBatch()` to hash all thumbnails in one call:

```go
// In imagestore.Put (or thumbnail.go QuickResize):
func storeThumbnails(original []byte, mime string) {
    thumbs := thumbnail.QuickResize(original, mime)
    
    // Collect all thumbnail bodies
    bodies := make([][]byte, 0, len(thumbs))
    for _, body := range thumbs {
        bodies = append(bodies, body)
    }
    
    // Batch-hash all thumbnails via SIMD-accelerated Zig
    hashes := hashBatch(bodies) // ZIG-1: single Zig call, ILP across inputs
    
    // Store each thumbnail with its hash
    for i, hash := range hashes {
        store.PutImage(ctx, hex.EncodeToString(hash[:]), mime, ...)
    }
}
```

#### Step 2: Crypto Batch Integration

During signature verification batches (e.g., verifying multiple keeper
transactions), use `Keccak256Batch()`:

```go
func verifyBatch(txs []Transaction) []bool {
    inputs := make([][]byte, len(txs))
    for i, tx := range txs {
        inputs[i] = tx.RawData
    }
    hashes := crypto.Keccak256Batch(inputs) // SIMD batch
    // ... verify signatures against hashes
}
```

### Files to modify:
- `backend/internal/imagestore/imagestore.go` — use hashBatch in Put
- `backend/internal/imagestore/thumbnail/thumbnail.go` — use hashBatch in QuickResize
- `backend/internal/indexer/handlers.go` — use Keccak256Batch for verification batches

---

## Summary: Implementation Order

| Order | Item | Effort | Impact | Blocked by |
|---|---|---|---|---|
| 1 | WH-3 Webhooks | 5 days | High (new feature) | None |
| 2 | GQL-4 GraphQL Subs | 10 days | High (new feature) | WH-3 (optional) |
| 3 | SSE-4 Wiring | 2 days | Medium (perf) | protoc regen |
| 4 | IMG-4 AVIF/WebP | 5 days | Medium (perf) | None |
| 5 | IMG-3 S3 Backend | 5 days | Medium (scale) | None |
| 6 | ZIG-1 Wiring | 2 days | Low (perf) | None |

**Total remaining effort: ~29 days (6 weeks with testing + review)**
