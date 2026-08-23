package api

import (
	"context"
	"errors"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// TxObserver is satisfied by *indexer.Runner. Wired by main via SetTxObserver
// because the runner is constructed before the HTTP layer is mounted.
type TxObserver interface {
	ObserveTx(ctx context.Context, hash common.Hash) (indexer.ObserveResult, error)
}

var txObserver atomic.Pointer[TxObserver]

// SetTxObserver installs the instant-lane indexer used by POST /api/v1/tx/observe.
// Atomic because main sets it while Fiber may already be serving.
func SetTxObserver(o TxObserver) { txObserver.Store(&o) }

func getTxObserver() TxObserver {
	if p := txObserver.Load(); p != nil {
		return *p
	}
	return nil
}

var txHashRE = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// registerTxObserve mounts POST /api/v1/tx/observe — the instant lane.
//
// The frontend calls this the moment a wallet returns a transaction hash.
// The server fetches the receipt and indexes the logs immediately (idempotent
// with the reorg-safe watcher), then publishes "tx-indexed" on the event
// spine so the page flips to "live" without waiting for the next watcher
// tick. No auth: the hash is public, the work is bounded (one receipt, one
// header), and the route is rate-limited per IP.
//
//	202 {"status":"pending"}       — not mined yet, client may retry
//	200 {"status":"indexed",...}   — logs dispatched (or already seen)
//	200 {"status":"reverted",...}  — mined but reverted; nothing to index
//	204                            — mined but touches no marketplace contract
func registerTxObserve(api fiber.Router, rl *ratelimit.Limiter) {
	api.Post("/tx/observe", tieredRateLimitMiddleware(rl, "observe", 60, time.Minute), func(c *fiber.Ctx) error {
		obs := getTxObserver()
		if obs == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "indexer not running"})
		}
		var body struct {
			Hash string `json:"hash"`
		}
		if err := c.BodyParser(&body); err != nil || !txHashRE.MatchString(body.Hash) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "hash must be a 0x-prefixed 32-byte hex string"})
		}
		ctx, cancel := context.WithTimeout(c.Context(), 12*time.Second)
		defer cancel()
		res, err := obs.ObserveTx(ctx, common.HexToHash(body.Hash))
		switch {
		case errors.Is(err, indexer.ErrTxPending):
			return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "pending", "hash": body.Hash})
		case errors.Is(err, indexer.ErrTxIrrelevant):
			return c.SendStatus(fiber.StatusNoContent)
		case err != nil:
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "could not index transaction", "hash": body.Hash})
		}
		return c.JSON(res)
	})
}

// registerEventCatalog mounts GET /api/v1/events/catalog — the machine-readable
// list of real-time event types and which transport carries each (WS,
// GraphQL subscriptions, Connect streams, webhooks). Source of truth is
// sse.EventCatalog; a test keeps every transport in agreement with it.
func registerEventCatalog(api fiber.Router) {
	api.Get("/events/catalog", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "public, max-age=300")
		return c.JSON(fiber.Map{
			"transports": fiber.Map{
				"ws":      "/ws (subscribe to channels; replay with {type:retry,data:{from_seq}})",
				"graphql": "/graphql/ws (subscriptions: listingUpdated, auctionUpdated, activityUpdated, notificationUpdated)",
				"connect": "Connect/gRPC MarketplaceService Subscribe* server streams",
				"webhook": "/api/v1/webhooks (outbound POST per event)",
			},
			"events": sse.EventCatalog,
		})
	})
}
