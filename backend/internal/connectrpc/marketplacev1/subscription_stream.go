// Package marketplacev1 — Connect-RPC streaming subscription handlers.
//
// SubscribeListings, SubscribeAuctions, SubscribeActivity and
// SubscribeNotifications are declared in marketplace.proto (v3.6 wave 6) and
// served through the generated MarketplaceService handler alongside the unary
// RPCs — generated clients and reflection consumers discover them like any
// other procedure. This file holds the handler bodies and the row → proto
// mappers; the message types live in marketplace.pb.go.
package marketplacev1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── Server-side streaming handler implementations ─────────────────────────────

// SubscribeListings streams listing-updated events to clients.
// Filters by optional collection and token_id.
func (s *Server) SubscribeListings(ctx context.Context, req *connect.Request[SubscribeListingsRequest], stream *connect.ServerStream[SubscribeListingsResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

	filterCollection := strings.ToLower(req.Msg.GetCollection())
	filterTokenID := req.Msg.GetTokenId()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-eventCh:
			if !ok {
				return nil
			}
			if ev.Type != "listing-updated" {
				continue
			}

			// Parse the listing from the event data.
			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			var row db.ListingRow
			if err := json.Unmarshal(data, &row); err != nil {
				continue
			}

			// Apply filters.
			if filterCollection != "" && !strings.EqualFold(row.Collection, filterCollection) {
				continue
			}
			if filterTokenID != "" && row.TokenID != filterTokenID {
				continue
			}

			// Convert to protobuf type and send.
			listing := listingRowToProto(&row)
			if err := stream.Send(&SubscribeListingsResponse{Listing: listing}); err != nil {
				return err
			}
		}
	}
}

// SubscribeAuctions streams auction-updated events to clients.
// Filters by optional auction_id.
func (s *Server) SubscribeAuctions(ctx context.Context, req *connect.Request[SubscribeAuctionsRequest], stream *connect.ServerStream[SubscribeAuctionsResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

	filterAuctionID := req.Msg.GetAuctionId()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-eventCh:
			if !ok {
				return nil
			}
			if ev.Type != "auction-updated" {
				continue
			}

			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			var row db.AuctionRow
			if err := json.Unmarshal(data, &row); err != nil {
				continue
			}

			if filterAuctionID != 0 && row.AuctionID != filterAuctionID {
				continue
			}

			auction := auctionRowToProto(&row)
			if err := stream.Send(&SubscribeAuctionsResponse{Auction: auction}); err != nil {
				return err
			}
		}
	}
}

// SubscribeActivity streams activity events to clients.
// Filters by optional address (matches the event's from/to/address field),
// collection, and token_id.
func (s *Server) SubscribeActivity(ctx context.Context, req *connect.Request[SubscribeActivityRequest], stream *connect.ServerStream[SubscribeActivityResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

	filterAddress := strings.ToLower(req.Msg.GetAddress())
	filterCollection := strings.ToLower(req.Msg.GetCollection())
	filterTokenID := req.Msg.GetTokenId()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-eventCh:
			if !ok {
				return nil
			}
			if ev.Type != "activity" {
				continue
			}

			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			var row db.ActivityRow
			if err := json.Unmarshal(data, &row); err != nil {
				continue
			}

			// Apply request filters.
			if filterCollection != "" && !strings.EqualFold(row.Collection, filterCollection) {
				continue
			}
			if filterTokenID != "" && row.TokenID != filterTokenID {
				continue
			}
			if filterAddress != "" {
				// Activity rows carry no address, but typed sse.ActivityEvent
				// payloads carry from/to (and some payloads an address field).
				var meta struct {
					From    string `json:"from"`
					To      string `json:"to"`
					Address string `json:"address"`
				}
				_ = json.Unmarshal(data, &meta)
				if !strings.EqualFold(meta.From, filterAddress) &&
					!strings.EqualFold(meta.To, filterAddress) &&
					!strings.EqualFold(meta.Address, filterAddress) {
					continue
				}
			}

			event := &ActivityEvent{
				Type:        row.Type,
				Collection:  row.Collection,
				TokenId:     row.TokenID,
				AmountWei:   row.AmountWei,
				TimestampMs: row.Timestamp.UnixMilli(),
				TxHash:      row.TxHash,
			}

			if err := stream.Send(&SubscribeActivityResponse{Event: event}); err != nil {
				return err
			}
		}
	}
}

// SubscribeNotifications streams notification events to clients.
//
// Notifications belong to ONE wallet, so this stream requires a caller
// authenticated as that wallet (JWT bearer token or session cookie) and
// refuses to stream any other address's notifications.
//
// API-key callers are deliberately rejected: an API key authenticates a
// machine principal ("apikey:<label>"), not a wallet, so there is no address
// it could legitimately subscribe to. Machine clients that need notification
// data should read it through the per-user REST/GraphQL endpoints, which
// carry their own authorization.
func (s *Server) SubscribeNotifications(ctx context.Context, req *connect.Request[SubscribeNotificationsRequest], stream *connect.ServerStream[SubscribeNotificationsResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

	// Notifications are per-user data, so the requested address must belong to
	// the authenticated caller. filterAddress arrives in the request body and
	// is attacker-controlled on its own; without this check any client could
	// subscribe with someone else's address and read their notifications.
	// Streaming RPCs never pass through the unary auth interceptor, so this
	// handler authenticates the caller itself (see Server.callerAddr).
	filterAddress := strings.ToLower(req.Msg.GetAddress())
	caller := s.callerAddr(ctx, req.Header())
	if caller == "" {
		return connect.NewError(connect.CodeUnauthenticated,
			fmt.Errorf("notification stream requires an authenticated caller"))
	}
	if filterAddress == "" {
		filterAddress = caller
	} else if filterAddress != caller {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("cannot subscribe to notifications for another address"))
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-eventCh:
			if !ok {
				return nil
			}
			if ev.Type != "notification" {
				continue
			}

			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}

			// Deliver ONLY notifications provably addressed to this subscriber.
			// This filter used to fail OPEN: an unmarshal error, or a payload
			// with no matching field, fell through to Send, so notifications
			// meant for one wallet reached every subscriber on the stream.
			//
			// The field is user_addr, NOT address: the payload is a marshalled
			// sse.NotificationEvent, whose recipient lives in User/UserAddr.
			// The other two consumers of this same event — ws/handler.go and
			// graphql/resolver.go — both match on UserAddr; matching anything
			// else here means matching nothing at all.
			var meta struct {
				UserAddr string `json:"user_addr"`
			}
			if err := json.Unmarshal(payload, &meta); err != nil {
				continue
			}
			if !strings.EqualFold(meta.UserAddr, filterAddress) {
				continue
			}

			if err := stream.Send(&SubscribeNotificationsResponse{Payload: payload}); err != nil {
				return err
			}
		}
	}
}

// ── Row-to-proto mapping helpers (duplicated from server.go helpers) ─────────
// These convert DB rows to the shared protobuf types (Listing, Auction, etc.).
// They mirror listingFromRow/auctionFromRow style but output proto types,
// not the GraphQL models.

func listingRowToProto(row *db.ListingRow) *Listing {
	l := &Listing{
		Collection:         row.Collection,
		TokenId:            row.TokenID,
		Seller:             row.Seller,
		PriceWei:           row.PriceWei,
		Amount:             row.Amount,
		Standard:           row.Standard,
		TxHash:             row.TxHash,
		Name:               row.Name,
		ImageUri:           row.ImageURI,
		CollectionVerified: row.CollectionVerified,
	}
	if !row.ExpiresAt.IsZero() {
		l.ExpiresAtMs = row.ExpiresAt.UnixMilli()
	}
	if !row.ListedAt.IsZero() {
		l.ListedAtMs = row.ListedAt.UnixMilli()
	}
	return l
}

func auctionRowToProto(row *db.AuctionRow) *Auction {
	a := &Auction{
		AuctionId:       row.AuctionID,
		Collection:      row.Collection,
		TokenId:         row.TokenID,
		Seller:          row.Seller,
		Standard:        row.Standard,
		ReservePriceWei: row.ReservePriceWei,
		HighestBidWei:   row.HighestBidWei,
		HighestBidder:   row.HighestBidder,
		MinIncrementBps: int32(row.MinIncrementBps),
		Status:          row.Status,
		CreateTx:        row.CreateTx,
		Name:            row.Name,
		ImageUri:        row.ImageURI,
		// Mirror listingRowToProto: streamed auctions must report the same
		// verified flag as ListAuctions.
		CollectionVerified: row.CollectionVerified,
	}
	if !row.StartsAt.IsZero() {
		a.StartsAtMs = row.StartsAt.UnixMilli()
	}
	if !row.EndsAt.IsZero() {
		a.EndsAtMs = row.EndsAt.UnixMilli()
	}
	return a
}
