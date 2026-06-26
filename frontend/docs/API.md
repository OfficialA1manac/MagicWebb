# API Reference

MagicWebb exposes a RESTful JSON API under `/api/v1/`. All endpoints are rate-limited to **60 requests per minute per IP**. Endpoints that modify user data require a **SIWE JWT** obtained via the `/auth/*` flow.

## OpenAPI Specification

A machine-readable OpenAPI 3.0.3 specification is available:

- **Raw YAML:** [`/docs/api.yaml`](/docs/api.yaml)
- **Swagger UI:** Use any OpenAPI viewer (e.g. [editor.swagger.io](https://editor.swagger.io)) with the YAML above.

## Authentication

### SIWE (Sign-In With Ethereum)

1. `GET /auth/nonce?address=0x...` — obtain a cryptographically random nonce (rate-limited: 20 req/min/IP).
2. Construct an [EIP-4361](https://eips.ethereum.org/EIPS/eip-4361) signing message containing the nonce and sign it with your wallet.
3. `POST /auth/verify` with `{address, message, signature}` — on success, returns a JWT and sets an HttpOnly session cookie.

### JWT Usage

Include the JWT in one of two ways:

| Method | Mechanism |
|--------|-----------|
| **Bearer header** | `Authorization: Bearer <token>` |
| **Session cookie** | `mw_s_<addr>=<token>` (auto-set by `/auth/verify`) |

## Endpoint Overview

### Listings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/listings` | List active listings (filterable by collection, seller) |
| GET | `/api/v1/listings/:collection/:id` | Get single listing |
| GET | `/api/v1/listings/:collection/:id/preflight` | Pre-flight checks (seller ownership, on-chain verify) |

### Auctions

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/auctions` | List auctions (filterable by collection, status) |
| GET | `/api/v1/auctions/:id` | Get single auction |
| GET | `/api/v1/auctions/:id/bids` | Get bids for an auction |
| GET | `/api/v1/server-time` | Current server Unix time (ms) |

### Offers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/offers` | List offers (filterable by collection, token, bidder, owner, status) |
| GET | `/api/v1/offers/:collection/:id/position` | Aggregated offer positions on a token |

### Collections

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/collections` | List tracked collections |
| GET | `/api/v1/collections/:address` | Collection detail + floor price, 24h volume, listed count |
| GET | `/api/v1/collections/:address/traits` | Trait value map for filtering |
| GET | `/api/v1/trending` | Trending collections by volume/activity score |

### Media

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/media?url=...` | Proxy external NFT images (SSRF-guarded) |
| GET | `/api/v1/img/:sha256` | Self-hosted image blob (immutable, 1-year cache) |
| POST | `/api/v1/img/retry?coll=...&id=...` | Trigger image self-hosting retry |

### Wallet

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/wallet/:addr/nfts` | NFTs owned by address (ERC-721 and ERC-1155) |

### Notifications 🔒

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/notifications` | List notifications + unread count |
| POST | `/api/v1/notifications/read` | Mark all notifications as read |

### Profiles

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/profile/:addr` | Get public profile |
| PUT | `/api/v1/profile/:addr` 🔒 | Update profile (name, bio, avatar, links) |

### Admin / Trust & Safety 🔒

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/reports` | Report a listing, collection, or user |
| POST | `/api/v1/admin/verify` | Admin: verify user profile |
| POST | `/api/v1/admin/collections/verify` | Admin: verify collection |

### Search

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/search?q=...` | Full-text search across tokens and collections |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/metrics` | Aggregate marketplace statistics + SSE health counters |
| GET | `/api/v1/activity` | Recent marketplace events (Listed, Sold, etc.) |

### Indexer

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/indexer/status` | Indexer state (last indexed block, event counts) |

### Infrastructure

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Liveness probe (DB + RPC) |
| GET | `/readyz` | Readiness probe (DB only) |
| GET | `/events` | Server-Sent Events stream (real-time updates) |

## Response Shapes

### Success

All successful responses return JSON. Arrays are always returned as `[]` (never `null`).

### Errors

Every error returns a JSON object with an `"error"` string:

```json
{ "error": "listing not found" }
```

Common HTTP status codes:

| Code | Meaning |
|------|---------|
| 200 | Success |
| 201 | Created |
| 204 | No content (success, no body) |
| 400 | Bad request (invalid params, missing fields) |
| 401 | Unauthorized (missing/invalid JWT) |
| 403 | Forbidden (admin-only endpoint) |
| 404 | Resource not found |
| 409 | Conflict (nonce already issued) |
| 429 | Rate limit exceeded |
| 500 | Internal server error |
| 502 | Bad gateway (upstream fetch failed) |
| 503 | Service unavailable (healthcheck failure) |

## Rate Limiting

- **`/api/v1/*`**: 60 requests per minute per IP
- **`/auth/*`**: 20 requests per minute per IP

Rate-limited requests return `429 Too Many Requests` with:
```json
{ "error": "rate limit exceeded" }
```

## Security Headers

Every response includes:
- `Content-Security-Policy`: strict same-origin policy
- `Strict-Transport-Security`: 2-year HSTS
- `X-Frame-Options: DENY`: clickjacking protection
- `X-Content-Type-Options: nosniff`: MIME-type locking
- `Referrer-Policy`: strict-origin-when-cross-origin
