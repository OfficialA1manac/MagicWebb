# WH-3: Webhook Marketplace Event Wiring

**Status:** ✅ Implemented | **Commit:** `3dde467` | **Category:** Integration / Webhooks

---

## Overview

WH-3 enables external services to receive real-time marketplace events via webhooks.
The dispatcher subscribes to the SSE Broadcaster and fans out 11 distinct marketplace
event types to registered webhook URLs, with per-config HMAC signing, retry with
exponential backoff, and delivery logging.

### Event Type Coverage

| Marketplace Event | SSE Type | Discriminator | Webhook Type |
|-------------------|----------|---------------|--------------|
| Listing created | `listing-updated` | `Listed` | `listing.created` |
| Listing updated/cancelled | `listing-updated` | `Cancelled`, `Transfer` | `listing.updated` |
| Listing sold | `listing-updated` | `Bought` | `listing.sold` |
| Auction created | `auction-updated` | `AuctionCreated` | `auction.created` |
| Bid placed | `auction-updated` | `BidPlaced`, `AuctionExtended` | `auction.bid` |
| Auction ended/cancelled | `auction-updated` | `AuctionCancelled`, `LoserRefunded` | `auction.ended` |
| Auction settled | `auction-updated` | `AuctionSettled` | `auction.settled` |
| Offer created | `offer-updated` | `OfferMade` | `offer.created` |
| Offer accepted | `offer-updated` | `OfferAccepted` | `offer.accepted` |
| Offer cancelled | `offer-updated` | `OfferRefunded` | `offer.cancelled` |
| Activity (global feed) | `activity` | — | `activity` |

---

## Architecture

```
Indexer handlers (handlers.go)
        │
        ▼ emit SSE events with "event" discriminator
┌─────────────────┐
│ SSE Broadcaster │
│   (local proc)  │
└────────┬────────┘
         │ SubscribeRaw() — receives ALL events
    ┌────▼───────────────────────────────────────┐
    │          Webhook Dispatcher                │
    │                                            │
    │  1. sseEventToWebhookType(ev)              │
    │     └─ Inspects ev.Type + data["event"]    │
    │        to resolve MarketplaceEventType      │
    │                                            │
    │  2. GetWebhookConfigsForEvent(eventType)   │
    │     └─ DB lookup for matching configs       │
    │                                            │
    │  3. Per-config: go deliver(ctx, cfg, payload)
    │     └─ HMAC signing + retry + delivery log  │
    └───────────────────────┬────────────────────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
         Webhook A     Webhook B     Webhook C
        (Discord)     (Slack)      (Custom URL)
```

### Event Discriminator Resolution

The key innovation in WH-3 is the `sseEventToWebhookType()` function, which resolves
the correct webhook event type by inspecting **both** the SSE type string and an
`"event"` discriminator field inside the Data payload. This means all 11 webhook
types can fire from just 6 SSE event types, without needing SSE-4 proto oneof
integration.

The discriminator extraction handles three data formats:
- `map[string]any` — from local indexer handler publishes
- `json.RawMessage` — from gRPC bridge cross-instance events
- `[]byte` — defense-in-depth for raw byte payloads

```go
func sseEventToWebhookType(ev sse.Event) MarketplaceEventType {
    eventDiscrim := extractEventDiscriminator(ev.Data)

    switch ev.Type {
    case "listing-updated":
        switch eventDiscrim {
        case "Listed":  return EventListingCreated
        case "Bought":  return EventListingSold
        case "Cancelled", "Transfer", "TransferSingle", "TransferBatch":
            return EventListingUpdated
        }
    // ... similar for auction-updated, offer-updated, activity
    }
}
```

---

## Dispatcher Lifecycle

The dispatcher runs as a long-lived background goroutine:

1. **Startup**: `main.go` calls `webhook.NewDispatcher(bcast, q, origin)` then `go dispatcher.Start(ctx)`
2. **Subscription**: `bcast.SubscribeRaw()` registers for ALL SSE events (no filtering at the broadcaster level)
3. **Event loop**: Each incoming event is dispatched to matching configs via individual goroutines
4. **Shutdown**: `ctx.Done()` triggers clean unsubscribe and exit

### Per-Delivery Properties

| Property | Value | Rationale |
|----------|-------|-----------|
| Timeout | 30 seconds | Per-delivery context; slow receivers don't block other deliveries |
| Goroutine | Per-config | Fan-out: all matching configs deliver concurrently |
| Payload marshaling | Once per event | `json.Marshal(ev.Data)` happens before fan-out |
| Retry | 3 attempts (5s → 30s → 120s) | Exponential backoff in `sender.go` |
| HMAC signing | Per-config secret | `X-Webhook-Signature` header via `sendJSONWithSecret()` |
| Delivery logging | Per attempt | `LogDelivery()` records status code, duration, attempt count |

---

## Configuration

### Webhook Config Model

```go
type WebhookConfig struct {
    ID        int64
    UserAddr  string                 // wallet address of the config owner
    URL       string                 // destination URL
    Secret    string                 // HMAC signing secret (never serialized)
    Events    []MarketplaceEventType // subscribed event types
    Active    bool                   // enable/disable
    CreatedAt time.Time
}
```

### API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/webhooks` | JWT | Register a new webhook |
| `GET` | `/api/v1/webhooks` | JWT | List user's webhooks |
| `DELETE` | `/api/v1/webhooks/:id` | JWT | Delete a webhook |
| `POST` | `/api/v1/webhooks/:id/test` | JWT | Send a test ping |

### Example Webhook Registration

```json
POST /api/v1/webhooks
{
    "url": "https://my-bot.example.com/webhook",
    "secret": "my-hmac-secret-key",
    "events": ["listing.created", "auction.settled", "offer.accepted"]
}
```

### Webhook Payload Format

```json
{
    "event": "listing.created",
    "timestamp": "2026-07-19T14:30:00Z",
    "instance": "magicwebb:50051",
    "data": {
        "event": "Listed",
        "collection": "0xabc...",
        "token_id": "42",
        "seller": "0xdef...",
        "price_wei": "1000000000000000000",
        "standard": "erc721",
        "amount": 1
    }
}
```

Headers:
- `Content-Type: application/json`
- `X-Webhook-Signature: sha256=<hmac-sha256-hex>`
- `X-MW-Instance: magicwebb:50051`

### Verifying Signatures

Receivers verify the `X-Webhook-Signature` header against the shared secret:

```
HMAC-SHA256(request_body, secret) == hex(signature_header_value)
```

---

## Files

| File | Purpose |
|------|---------|
| `backend/internal/webhook/dispatcher.go` | Event type mapping, SSE subscriber, fan-out dispatcher |
| `backend/internal/webhook/sender.go` | HTTP delivery with retry, HMAC signing, format support |
| `backend/internal/db/queries.go` | `GetWebhookConfigsForEvent()`, `LogDelivery()` |
| `backend/internal/api/webhooks.go` | REST CRUD endpoints for webhook configs |
| `backend/internal/db/migrations/032_webhooks.sql` | `webhook_configs` and `webhook_delivery_log` tables |
| `backend/cmd/server/main.go` | Dispatcher creation and lifecycle start |

---

## Excluded Events

The following SSE events are explicitly NOT forwarded as webhooks:

| SSE Type | Reason |
|----------|--------|
| `notification` | User-targeted (private) — use user-scoped WebSocket channels instead |
| `rpc-health` | Infrastructure metric — not a marketplace event |

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| No webhooks firing | Dispatcher not started or broadcaster is nil | Check startup logs for "webhook: dispatcher started" |
| Webhook fires with wrong event type | Discriminator not in Data payload | Verify indexer handler emits `"event"` key in Data |
| Delivery fails with timeout | Receiver too slow (>30s) | Optimize receiver or increase per-delivery timeout |
| HMAC verification fails | Secret mismatch or clock skew | Verify shared secret matches; check system clock |
| `rpc-health` events appearing | Discriminator resolution for unknown types | Handled — unknown SSE types return `""` and are silently skipped |
