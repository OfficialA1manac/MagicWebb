// Package marketplacev1 — Connect-RPC streaming subscription handlers.
//
// This file provides the client and server streaming implementations for the
// SubscribeListings, SubscribeAuctions, SubscribeActivity, and
// SubscribeNotifications RPCs. These are registered as separate streaming
// procedures on the /marketplace.v1.MarketplaceService/ path alongside the
// existing unary RPCs.
package marketplacev1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// Streaming RPC procedure names (match the /marketplace.v1.MarketplaceService/
// path prefix used by the unary handler).
const (
	ProcedureSubscribeListings       = "/marketplace.v1.MarketplaceService/SubscribeListings"
	ProcedureSubscribeAuctions       = "/marketplace.v1.MarketplaceService/SubscribeAuctions"
	ProcedureSubscribeActivity       = "/marketplace.v1.MarketplaceService/SubscribeActivity"
	ProcedureSubscribeNotifications = "/marketplace.v1.MarketplaceService/SubscribeNotifications"
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
func (s *Server) SubscribeActivity(ctx context.Context, req *connect.Request[SubscribeActivityRequest], stream *connect.ServerStream[SubscribeActivityResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

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
func (s *Server) SubscribeNotifications(ctx context.Context, req *connect.Request[SubscribeNotificationsRequest], stream *connect.ServerStream[SubscribeNotificationsResponse]) error {
	if s.bcast == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("broadcaster not available"))
	}

	eventCh, cancel, ok := s.bcast.SubscribeRaw()
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many subscribers"))
	}
	defer cancel()

	filterAddress := strings.ToLower(req.Msg.GetAddress())

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

			// If address filter is set, try to check if this notification is for them.
			// The event payload should contain an "address" or "user" field.
			if filterAddress != "" {
				var meta struct {
					Address string `json:"address"`
				}
				if err := json.Unmarshal(payload, &meta); err == nil && meta.Address != "" && !strings.EqualFold(meta.Address, filterAddress) {
					continue
				}
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
	}
	if !row.StartsAt.IsZero() {
		a.StartsAtMs = row.StartsAt.UnixMilli()
	}
	if !row.EndsAt.IsZero() {
		a.EndsAtMs = row.EndsAt.UnixMilli()
	}
	return a
}

// ── Connect-RPC streaming client ────────────────────────────────────────────

// SubscribeListingsClient is a convenience wrapper around the generic
// Connect-RPC streaming client. Callers receive listings via Receive().
type SubscribeListingsClient struct {
	client *connect.Client[SubscribeListingsRequest, SubscribeListingsResponse]
}

// NewSubscribeListingsClient creates a streaming client for SubscribeListings.
func NewSubscribeListingsClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) *SubscribeListingsClient {
	return &SubscribeListingsClient{
		client: connect.NewClient[SubscribeListingsRequest, SubscribeListingsResponse](
			httpClient,
			baseURL+ProcedureSubscribeListings,
			connect.WithClientOptions(opts...),
		),
	}
}

// CallStream opens a server-streaming connection and returns an iterator.
func (c *SubscribeListingsClient) CallStream(ctx context.Context, req *connect.Request[SubscribeListingsRequest]) (*connect.ServerStreamForClient[SubscribeListingsResponse], error) {
	return c.client.CallServerStream(ctx, req)
}

// SubscribeAuctionsClient wraps the SubscribeAuctions streaming RPC.
type SubscribeAuctionsClient struct {
	client *connect.Client[SubscribeAuctionsRequest, SubscribeAuctionsResponse]
}

// NewSubscribeAuctionsClient creates a streaming client for SubscribeAuctions.
func NewSubscribeAuctionsClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) *SubscribeAuctionsClient {
	return &SubscribeAuctionsClient{
		client: connect.NewClient[SubscribeAuctionsRequest, SubscribeAuctionsResponse](
			httpClient,
			baseURL+ProcedureSubscribeAuctions,
			connect.WithClientOptions(opts...),
		),
	}
}

func (c *SubscribeAuctionsClient) CallStream(ctx context.Context, req *connect.Request[SubscribeAuctionsRequest]) (*connect.ServerStreamForClient[SubscribeAuctionsResponse], error) {
	return c.client.CallServerStream(ctx, req)
}

// SubscribeActivityClient wraps the SubscribeActivity streaming RPC.
type SubscribeActivityClient struct {
	client *connect.Client[SubscribeActivityRequest, SubscribeActivityResponse]
}

func NewSubscribeActivityClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) *SubscribeActivityClient {
	return &SubscribeActivityClient{
		client: connect.NewClient[SubscribeActivityRequest, SubscribeActivityResponse](
			httpClient,
			baseURL+ProcedureSubscribeActivity,
			connect.WithClientOptions(opts...),
		),
	}
}

func (c *SubscribeActivityClient) CallStream(ctx context.Context, req *connect.Request[SubscribeActivityRequest]) (*connect.ServerStreamForClient[SubscribeActivityResponse], error) {
	return c.client.CallServerStream(ctx, req)
}

// SubscribeNotificationsClient wraps the SubscribeNotifications streaming RPC.
type SubscribeNotificationsClient struct {
	client *connect.Client[SubscribeNotificationsRequest, SubscribeNotificationsResponse]
}

func NewSubscribeNotificationsClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) *SubscribeNotificationsClient {
	return &SubscribeNotificationsClient{
		client: connect.NewClient[SubscribeNotificationsRequest, SubscribeNotificationsResponse](
			httpClient,
			baseURL+ProcedureSubscribeNotifications,
			connect.WithClientOptions(opts...),
		),
	}
}

func (c *SubscribeNotificationsClient) CallStream(ctx context.Context, req *connect.Request[SubscribeNotificationsRequest]) (*connect.ServerStreamForClient[SubscribeNotificationsResponse], error) {
	return c.client.CallServerStream(ctx, req)
}

// ── Streaming handler constructors ────────────────────────────────────────────
// Each handler is registered on the same path prefix as the unary handler.

// NewSubscribeListingsHandler creates an HTTP handler for the SubscribeListings RPC.
func NewSubscribeListingsHandler(svc *Server, opts ...connect.HandlerOption) (string, *connect.Handler) {
	return ProcedureSubscribeListings, connect.NewServerStreamHandler(
		ProcedureSubscribeListings,
		svc.SubscribeListings,
		append(opts, connect.WithHandlerOptions(opts...))...,
	)
}

// NewSubscribeAuctionsHandler creates an HTTP handler for the SubscribeAuctions RPC.
func NewSubscribeAuctionsHandler(svc *Server, opts ...connect.HandlerOption) (string, *connect.Handler) {
	return ProcedureSubscribeAuctions, connect.NewServerStreamHandler(
		ProcedureSubscribeAuctions,
		svc.SubscribeAuctions,
		append(opts, connect.WithHandlerOptions(opts...))...,
	)
}

// NewSubscribeActivityHandler creates an HTTP handler for the SubscribeActivity RPC.
func NewSubscribeActivityHandler(svc *Server, opts ...connect.HandlerOption) (string, *connect.Handler) {
	return ProcedureSubscribeActivity, connect.NewServerStreamHandler(
		ProcedureSubscribeActivity,
		svc.SubscribeActivity,
		append(opts, connect.WithHandlerOptions(opts...))...,
	)
}

// NewSubscribeNotificationsHandler creates an HTTP handler for the SubscribeNotifications RPC.
func NewSubscribeNotificationsHandler(svc *Server, opts ...connect.HandlerOption) (string, *connect.Handler) {
	return ProcedureSubscribeNotifications, connect.NewServerStreamHandler(
		ProcedureSubscribeNotifications,
		svc.SubscribeNotifications,
		append(opts, connect.WithHandlerOptions(opts...))...,
	)
}

// GetField helpers to satisfy the proto message interface for streaming types.
// These implement the protoreflect.ProtoMessage interface minimally so that
// Connect-RPC can use them with JSON codec.

func (x *SubscribeListingsRequest) GetCollection() string     { return x.Collection }
func (x *SubscribeListingsRequest) GetTokenId() string        { return x.TokenID }
func (x *SubscribeAuctionsRequest) GetAuctionId() int64       { return x.AuctionID }
func (x *SubscribeActivityRequest) GetAddress() string        { return x.Address }
func (x *SubscribeActivityRequest) GetCollection() string     { return x.Collection }
func (x *SubscribeActivityRequest) GetTokenId() string        { return x.TokenID }
func (x *SubscribeNotificationsRequest) GetAddress() string   { return x.Address }
func (x *SubscribeListingsResponse) GetListing() *Listing     { return x.Listing }
func (x *SubscribeAuctionsResponse) GetAuction() *Auction     { return x.Auction }
func (x *SubscribeActivityResponse) GetEvent() *ActivityEvent { return x.Event }
func (x *SubscribeNotificationsResponse) GetPayload() []byte  { return x.Payload }

// ProtoMessage is required by protoreflect for Connect-RPC compatibility.
func (*SubscribeListingsRequest) ProtoMessage()       {}
func (*SubscribeListingsResponse) ProtoMessage()      {}
func (*SubscribeAuctionsRequest) ProtoMessage()        {}
func (*SubscribeAuctionsResponse) ProtoMessage()       {}
func (*SubscribeActivityRequest) ProtoMessage()        {}
func (*SubscribeActivityResponse) ProtoMessage()       {}
func (*SubscribeNotificationsRequest) ProtoMessage()   {}
func (*SubscribeNotificationsResponse) ProtoMessage()  {}
func (*SubscribeListingsRequest) Reset()               {}
func (*SubscribeListingsResponse) Reset()              {}
func (*SubscribeAuctionsRequest) Reset()                {}
func (*SubscribeAuctionsResponse) Reset()               {}
func (*SubscribeActivityRequest) Reset()                {}
func (*SubscribeActivityResponse) Reset()               {}
func (*SubscribeNotificationsRequest) Reset()           {}
func (*SubscribeNotificationsResponse) Reset()          {}
func (x *SubscribeListingsRequest) String() string        { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeListingsResponse) String() string       { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeAuctionsRequest) String() string         { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeAuctionsResponse) String() string        { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeActivityRequest) String() string         { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeActivityResponse) String() string        { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeNotificationsRequest) String() string    { return fmt.Sprintf("%+v", *x) }
func (x *SubscribeNotificationsResponse) String() string   { return fmt.Sprintf("%+v", *x) }
