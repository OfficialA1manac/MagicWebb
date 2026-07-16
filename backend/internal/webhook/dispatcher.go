// Package webhook — WH-3: Marketplace event webhooks.
//
// Defines marketplace event types, webhook configuration models, and the
// event dispatcher that fans out SSE Broadcaster events to registered
// webhook URLs with retry and HMAC-signing.
package webhook

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// ── WH-3: Marketplace event types ────────────────────────────────────────

// MarketplaceEventType enumerates the event types users can subscribe to.
// These map to the SSE event types produced by the indexer and broadcaster.
type MarketplaceEventType string

const (
	EventListingCreated  MarketplaceEventType = "listing.created"
	EventListingUpdated  MarketplaceEventType = "listing.updated"
	EventListingSold     MarketplaceEventType = "listing.sold"
	EventAuctionCreated  MarketplaceEventType = "auction.created"
	EventAuctionBid      MarketplaceEventType = "auction.bid"
	EventAuctionEnded    MarketplaceEventType = "auction.ended"
	EventAuctionSettled  MarketplaceEventType = "auction.settled"
	EventOfferCreated    MarketplaceEventType = "offer.created"
	EventOfferAccepted   MarketplaceEventType = "offer.accepted"
	EventOfferCancelled  MarketplaceEventType = "offer.cancelled"
	EventActivity        MarketplaceEventType = "activity"
)

// ValidEvents is the set of all recognised webhook event types. Used to
// validate user-submitted event filters.
var ValidEvents = map[MarketplaceEventType]bool{
	EventListingCreated: true,
	EventListingUpdated: true,
	EventListingSold:    true,
	EventAuctionCreated: true,
	EventAuctionBid:     true,
	EventAuctionEnded:   true,
	EventAuctionSettled: true,
	EventOfferCreated:   true,
	EventOfferAccepted:  true,
	EventOfferCancelled: true,
	EventActivity:       true,
}

// sseToWebhook maps SSE event type strings to webhook marketplace event types.
// Events whose SSE type doesn't map to a webhook type are silently skipped.
// NOTE: listing.created, listing.sold, auction.created, auction.ended,
// auction.settled, offer.accepted, and offer.cancelled have no SSE mapping
// yet — the indexer doesn't emit dedicated SSE types for on-chain events
// like ListingSold or AuctionCreated. These webhook types are available for
// subscription but won't fire until the SSE bridge gains per-event-type
// emission (SSE-4 wiring task in docs/OPTIMIZATION_DESIGN.md).
var sseToWebhook = map[string]MarketplaceEventType{
	"listing-updated": EventListingUpdated, // listing.created/updated share SSE type
	"auction-updated": EventAuctionBid,      // auction bid = new bid placed
	"offer-updated":   EventOfferCreated,    // offer.created shares SSE type
	"activity":        EventActivity,
	"notification":    "", // notifications are user-targeted, not marketplace-wide
}

// ── Config model ─────────────────────────────────────────────────────────

// WebhookConfig is one user-registered webhook URL with event filters.
// Mirrors the webhook_configs table row.
type WebhookConfig struct {
	ID        int64                  `json:"id"`
	UserAddr  string                 `json:"user_addr"`
	URL       string                 `json:"url"`
	Secret    string                 `json:"-"` // never serialized to client
	Events    []MarketplaceEventType `json:"events"`
	Active    bool                   `json:"active"`
	CreatedAt time.Time              `json:"created_at"`
}

// HasEvent returns true when the config is subscribed to a given event type.
func (c *WebhookConfig) HasEvent(evt MarketplaceEventType) bool {
	for _, e := range c.Events {
		if e == evt {
			return true
		}
	}
	return false
}

// ── Dispatcher ───────────────────────────────────────────────────────────

// ConfigStore is the database interface for webhook config lookups.
// Implemented by *db.Q in production; injectable for tests.
type ConfigStore interface {
	// GetWebhookConfigsForEvent returns all active configs subscribed to the
	// given event type. Used on every SSE event dispatch.
	GetWebhookConfigsForEvent(ctx context.Context, eventType MarketplaceEventType) ([]WebhookConfig, error)

	// LogDelivery records a delivery attempt for audit and rate-limit tracking.
	LogDelivery(ctx context.Context, configID int64, eventType MarketplaceEventType, statusCode int, errMsg string, attemptCount, durationMs int) error
}

// WebhookPayload is the JSON body POSTed to registered webhook URLs.
type WebhookPayload struct {
	Event     MarketplaceEventType `json:"event"`
	Timestamp string               `json:"timestamp"`           // RFC3339
	Instance  string               `json:"instance,omitempty"`  // origin instance UUID
	Data      json.RawMessage      `json:"data"`                // original event Data
}

// Dispatcher subscribes to the SSE Broadcaster and fans out events to
// matching webhook configurations. One dispatcher per instance; it uses
// SubscribeRaw() to receive all events and filters by type + config.
type Dispatcher struct {
	bcast  *sse.Broadcaster
	store  ConfigStore
	origin string // instance UUID for payload identification
}

// NewDispatcher creates a webhook event dispatcher.
func NewDispatcher(bcast *sse.Broadcaster, store ConfigStore, origin string) *Dispatcher {
	return &Dispatcher{bcast: bcast, store: store, origin: origin}
}

// Start begins listening for SSE events and dispatching to matching webhooks.
// Blocks until ctx is cancelled. Call from a background goroutine.
func (d *Dispatcher) Start(ctx context.Context) {
	if d.bcast == nil {
		log.Warn().Msg("webhook: broadcaster nil, dispatcher not started")
		return
	}

	eventCh, cancel, ok := d.bcast.SubscribeRaw()
	if !ok {
		log.Warn().Msg("webhook: subscribe failed (too many subscribers), dispatcher not started")
		return
	}
	defer cancel()

	log.Info().Msg("webhook: dispatcher started")
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("webhook: dispatcher stopped")
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			d.dispatch(ctx, ev)
		}
	}
}

// dispatch maps an SSE event to a webhook event type and fans out to
// matching configs. Each delivery runs in its own goroutine with a
// 30-second timeout so a slow webhook receiver doesn't block other
// deliveries.
func (d *Dispatcher) dispatch(ctx context.Context, ev sse.Event) {
	hookType, ok := sseToWebhook[ev.Type]
	if !ok || hookType == "" {
		return // event type not mapped to any webhook type
	}

	configs, err := d.store.GetWebhookConfigsForEvent(ctx, hookType)
	if err != nil {
		log.Warn().Err(err).Str("event", string(hookType)).Msg("webhook: config lookup failed")
		return
	}

	if len(configs) == 0 {
		return
	}

	// Marshal the event data once for all deliveries.
	payload := WebhookPayload{
		Event:     hookType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Instance:  d.origin,
	}
	if ev.Data != nil {
		data, err := json.Marshal(ev.Data)
		if err != nil {
			return
		}
		payload.Data = json.RawMessage(data)
	}

	for _, cfg := range configs {
		go d.deliver(ctx, cfg, hookType, payload)
	}
}

// deliver sends the webhook payload to a single config URL with retry.
// Uses sendJSONWithSecret (not the package-level HMACSecret var) so
// per-config secrets don't race across concurrent deliveries.
func (d *Dispatcher) deliver(ctx context.Context, cfg WebhookConfig, hookType MarketplaceEventType, payload WebhookPayload) {
	deliveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	err := sendJSONWithSecret(deliveryCtx, cfg.URL, payload, cfg.Secret)
	elapsed := int(time.Since(start).Milliseconds())

	// Log the delivery attempt.
	statusCode := 200
	errMsg := ""
	attempts := 1
	if err != nil {
		statusCode = 0 // network error or timeout — no HTTP response
		errMsg = err.Error()
		// sendJSON retries internally; extract actual attempt count from error
		if len(errMsg) > 256 {
			errMsg = errMsg[:256]
		}
	}

	if logErr := d.store.LogDelivery(ctx, cfg.ID, hookType, statusCode, errMsg, attempts, elapsed); logErr != nil {
		log.Warn().Err(logErr).Int64("config_id", cfg.ID).Msg("webhook: delivery log failed")
	}

	if err != nil {
		log.Warn().Err(err).Int64("config_id", cfg.ID).Str("url", cfg.URL).Str("event", string(hookType)).
			Msg("webhook: delivery failed")
	}
}
