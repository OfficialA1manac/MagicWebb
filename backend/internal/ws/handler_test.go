package ws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/valyala/fasthttp"

	marketplacev1 "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// mockMarketplaceClient implements marketplacev1connect.MarketplaceServiceClient
// for testing WS action handlers without real HTTP calls.
type mockMarketplaceClient struct {
	getListingFunc func(ctx context.Context, req *connect.Request[marketplacev1.GetListingRequest]) (*connect.Response[marketplacev1.GetListingResponse], error)
	getAuctionFunc  func(ctx context.Context, req *connect.Request[marketplacev1.GetAuctionRequest]) (*connect.Response[marketplacev1.GetAuctionResponse], error)
	getOfferFunc    func(ctx context.Context, req *connect.Request[marketplacev1.GetOfferRequest]) (*connect.Response[marketplacev1.GetOfferResponse], error)
	getTokenFunc   func(ctx context.Context, req *connect.Request[marketplacev1.GetTokenRequest]) (*connect.Response[marketplacev1.GetTokenResponse], error)
}

func (m *mockMarketplaceClient) GetListing(ctx context.Context, req *connect.Request[marketplacev1.GetListingRequest]) (*connect.Response[marketplacev1.GetListingResponse], error) {
	if m.getListingFunc != nil {
		return m.getListingFunc(ctx, req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (m *mockMarketplaceClient) GetAuction(ctx context.Context, req *connect.Request[marketplacev1.GetAuctionRequest]) (*connect.Response[marketplacev1.GetAuctionResponse], error) {
	if m.getAuctionFunc != nil {
		return m.getAuctionFunc(ctx, req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (m *mockMarketplaceClient) GetOffer(ctx context.Context, req *connect.Request[marketplacev1.GetOfferRequest]) (*connect.Response[marketplacev1.GetOfferResponse], error) {
	if m.getOfferFunc != nil {
		return m.getOfferFunc(ctx, req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (m *mockMarketplaceClient) GetToken(ctx context.Context, req *connect.Request[marketplacev1.GetTokenRequest]) (*connect.Response[marketplacev1.GetTokenResponse], error) {
	if m.getTokenFunc != nil {
		return m.getTokenFunc(ctx, req)
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

// Stub implementations for interface methods not exercised by WS tests.
func (m *mockMarketplaceClient) ListCollections(ctx context.Context, req *connect.Request[marketplacev1.ListCollectionsRequest]) (*connect.ServerStreamForClient[marketplacev1.Collection], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) GetCollection(ctx context.Context, req *connect.Request[marketplacev1.GetCollectionRequest]) (*connect.Response[marketplacev1.GetCollectionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) ListListings(ctx context.Context, req *connect.Request[marketplacev1.ListListingsRequest]) (*connect.ServerStreamForClient[marketplacev1.Listing], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) ListAuctions(ctx context.Context, req *connect.Request[marketplacev1.ListAuctionsRequest]) (*connect.ServerStreamForClient[marketplacev1.Auction], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) GetActivity(ctx context.Context, req *connect.Request[marketplacev1.GetActivityRequest]) (*connect.Response[marketplacev1.GetActivityResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) ListOffers(ctx context.Context, req *connect.Request[marketplacev1.ListOffersRequest]) (*connect.ServerStreamForClient[marketplacev1.Offer], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) GetWalletNFTs(ctx context.Context, req *connect.Request[marketplacev1.GetWalletNFTsRequest]) (*connect.Response[marketplacev1.GetWalletNFTsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) GetProfile(ctx context.Context, req *connect.Request[marketplacev1.GetProfileRequest]) (*connect.Response[marketplacev1.GetProfileResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) Search(ctx context.Context, req *connect.Request[marketplacev1.SearchRequest]) (*connect.Response[marketplacev1.SearchResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
func (m *mockMarketplaceClient) GetMetrics(ctx context.Context, req *connect.Request[marketplacev1.GetMetricsRequest]) (*connect.Response[marketplacev1.GetMetricsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

// ── dispatchAction ──────────────────────────────────────────────────────────

func TestDispatchAction_NilClient(t *testing.T) {
	h := &Handler{client: nil}
	conn := newTestConn()
	h.dispatchAction(conn, ActionData{Action: "get_listing"})

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
	var ack AckData
	if err := json.Unmarshal(msg.Data, &ack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ack.Status != "error" || ack.Message != "server not ready" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
}

func TestDispatchAction_UnknownAction(t *testing.T) {
	conn := newTestConn()
	h := &Handler{client: &mockMarketplaceClient{}}
	h.dispatchAction(conn, ActionData{Action: "fly_to_the_moon"})

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
	var ack AckData
	if err := json.Unmarshal(msg.Data, &ack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ack.Message != "unknown action: fly_to_the_moon" {
		t.Fatalf("unexpected message: %s", ack.Message)
	}
}

// ── handleGetListing ────────────────────────────────────────────────────────

func TestHandleGetListing_InvalidParams(t *testing.T) {
	h := &Handler{}
	conn := newTestConn()
	h.handleGetListing(conn, json.RawMessage(`{}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
}

func TestHandleGetListing_Success(t *testing.T) {
	now := time.Now()
	client := &mockMarketplaceClient{
		getListingFunc: func(_ context.Context, req *connect.Request[marketplacev1.GetListingRequest]) (*connect.Response[marketplacev1.GetListingResponse], error) {
			if req.Msg.Collection != "0xabc" || req.Msg.TokenId != "1" {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
			}
			return connect.NewResponse(&marketplacev1.GetListingResponse{
				Collection: "0xabc",
				TokenId:    "1",
				Seller:     "0xseller",
				PriceWei:   "1000000000000000000",
				Amount:     1,
				Standard:   "erc721",
				ExpiresAtMs: now.Add(24 * time.Hour).UnixMilli(),
				ListedAtMs:  now.UnixMilli(),
				TxHash:     "0xtx",
				Name:       "MyToken",
				ImageUri:   "https://example.com/img.png",
			}), nil
		},
	}
	h := &Handler{client: client}
	conn := newTestConn()
	h.handleGetListing(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}
}

func TestHandleGetListing_NotFound(t *testing.T) {
	client := &mockMarketplaceClient{
		getListingFunc: func(_ context.Context, _ *connect.Request[marketplacev1.GetListingRequest]) (*connect.Response[marketplacev1.GetListingResponse], error) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("listing not found"))
		},
	}
	h := &Handler{client: client}
	conn := newTestConn()
	h.handleGetListing(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
}

// ── handleGetAuction ────────────────────────────────────────────────────────

func TestHandleGetAuction_InvalidParams(t *testing.T) {
	h := &Handler{}
	conn := newTestConn()
	h.handleGetAuction(conn, json.RawMessage(`{}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
}

func TestHandleGetAuction_Success(t *testing.T) {
	now := time.Now()
	client := &mockMarketplaceClient{
		getAuctionFunc: func(_ context.Context, req *connect.Request[marketplacev1.GetAuctionRequest]) (*connect.Response[marketplacev1.GetAuctionResponse], error) {
			if req.Msg.AuctionId != 42 {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
			}
			return connect.NewResponse(&marketplacev1.GetAuctionResponse{
				AuctionId:       42,
				Collection:      "0xcol",
				TokenId:         "1",
				Seller:          "0xseller",
				Standard:        "erc721",
				ReservePriceWei: "5000000000000000000",
				HighestBidWei:   "6000000000000000000",
				HighestBidder:   "0xbidder",
				MinIncrementBps: 100,
				StartsAtMs:      now.UnixMilli(),
				EndsAtMs:        now.Add(24 * time.Hour).UnixMilli(),
				Status:          "active",
				CreateTx:        "0xtx",
				Name:            "Auction 42",
			}), nil
		},
	}
	h := &Handler{client: client}
	conn := newTestConn()
	h.handleGetAuction(conn, json.RawMessage(`{"auction_id":42}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}
}

// ── handleGetOffer ──────────────────────────────────────────────────────────

func TestHandleGetOffer_InvalidParams(t *testing.T) {
	h := &Handler{}
	conn := newTestConn()
	h.handleGetOffer(conn, json.RawMessage(`{}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
}

func TestHandleGetOffer_Success(t *testing.T) {
	now := time.Now()
	client := &mockMarketplaceClient{
		getOfferFunc: func(_ context.Context, req *connect.Request[marketplacev1.GetOfferRequest]) (*connect.Response[marketplacev1.GetOfferResponse], error) {
			if req.Msg.OfferId != "42" {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
			}
			return connect.NewResponse(&marketplacev1.GetOfferResponse{
				OfferId:    "42",
				Bidder:     "0xbidder",
				Collection: "0xcol",
				TokenId:    "1",
				AmountWei:  "1000000000000000000",
				FeeWei:     "10000000000000000",
				Units:      1,
				Standard:   "erc721",
				ExpiresAtMs: now.Add(7 * 24 * time.Hour).UnixMilli(),
				Status:     "pending",
				MakeTx:     "0xmtx",
				CreatedAtMs: now.UnixMilli(),
			}), nil
		},
	}
	h := &Handler{client: client}
	conn := newTestConn()
	h.handleGetOffer(conn, json.RawMessage(`{"offer_id":"42"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}
}

// ── handleGetToken ──────────────────────────────────────────────────────────

func TestHandleGetToken_InvalidParams(t *testing.T) {
	h := &Handler{}
	conn := newTestConn()
	h.handleGetToken(conn, json.RawMessage(`{}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}
}

func TestHandleGetToken_Success(t *testing.T) {
	now := time.Now()
	client := &mockMarketplaceClient{
		getTokenFunc: func(_ context.Context, req *connect.Request[marketplacev1.GetTokenRequest]) (*connect.Response[marketplacev1.GetTokenResponse], error) {
			if req.Msg.Collection != "0xabc" || req.Msg.TokenId != "1" {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
			}
			return connect.NewResponse(&marketplacev1.GetTokenResponse{
				Collection:   "0xabc",
				TokenId:      "1",
				Name:         "My Token",
				Description:  "A cool token",
				ImageUri:     "https://img.com/1.png",
				AnimationUri: "",
				MetadataUri:  "https://meta.com/1.json",
				FetchedAtMs:  now.UnixMilli(),
			}), nil
		},
	}
	h := &Handler{client: client}
	conn := newTestConn()
	h.handleGetToken(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}
}

// ── acquireIP / releaseIP / ActiveConns ─────────────────────────────────────

func TestAcquireIP_Basic(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	if !h.acquireIP("1.2.3.4") {
		t.Fatal("first acquire should succeed")
	}
	if !h.acquireIP("1.2.3.4") {
		t.Fatal("second acquire should succeed (limit is 20)")
	}
}

func TestAcquireIP_ExceedsLimit(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	// Acquire up to the limit
	for i := 0; i < wsPerIPLimit; i++ {
		if !h.acquireIP("1.2.3.4") {
			t.Fatalf("acquire %d should succeed", i+1)
		}
	}
	// Next one should fail
	if h.acquireIP("1.2.3.4") {
		t.Fatal("acquire beyond limit should fail")
	}
}

func TestAcquireIP_DifferentIPs(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	// Fill IP A to capacity
	for i := 0; i < wsPerIPLimit; i++ {
		h.acquireIP("10.0.0.1")
	}
	// IP B should still succeed
	if !h.acquireIP("10.0.0.2") {
		t.Fatal("different IP should succeed")
	}
}

func TestReleaseIP_Decrements(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	h.acquireIP("1.2.3.4")
	h.releaseIP("1.2.3.4")

	// After release, the IP counter should be gone (hit 0)
	h.mu.RLock()
	_, ok := h.ipCounters["1.2.3.4"]
	h.mu.RUnlock()
	if ok {
		t.Fatal("IP counter should be removed after release to 0")
	}
}

func TestReleaseIP_UnacquiredIP(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	// Release without acquire should not panic
	h.releaseIP("9.9.9.9")
}

func TestActiveConns(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	if n := h.ActiveConns(); n != 0 {
		t.Fatalf("ActiveConns = %d, want 0", n)
	}

	conn := newTestConn()
	h.mu.Lock()
	h.conns[conn.id] = conn
	h.mu.Unlock()

	if n := h.ActiveConns(); n != 1 {
		t.Fatalf("ActiveConns = %d, want 1", n)
	}
}

// ── BroadcastTo smoke test ──────────────────────────────────────────────────

func TestBroadcastTo_DeliversToAll(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	conn1 := newTestConn()
	conn2 := newTestConn()
	h.mu.Lock()
	h.conns["c1"] = conn1
	h.conns["c2"] = conn2
	h.mu.Unlock()

	// Connections without subscriptions receive all events (backward-compat).
	h.BroadcastTo(sse.Event{Type: "test-event", Data: map[string]interface{}{"msg": "hello", "collection": "0xABC"}})

	for _, conn := range []*Connection{conn1, conn2} {
		msg := recvWS(t, conn)
		if msg.Type != MessageType("test-event") {
			t.Fatalf("type = %s, want test-event", msg.Type)
		}
	}
}

func TestBroadcastTo_RespectsSubscriptions(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	conn1 := newTestConn()
	conn1.subscriptions = map[string]struct{}{"token:0xABC:1": {}}
	conn2 := newTestConn()
	conn2.subscriptions = map[string]struct{}{"token:0xDEF:2": {}}

	h.mu.Lock()
	h.conns["c1"] = conn1
	h.conns["c2"] = conn2
	h.mu.Unlock()

	// Event for token 0xABC:1 — only conn1 should receive it.
	h.BroadcastTo(sse.Event{Type: "listing-updated", Data: map[string]interface{}{
		"collection": "0xABC",
		"token_id":   "1",
	}})

	// conn1 receives, conn2 does not
	msg1 := recvWS(t, conn1)
	if msg1.Type != MessageType("listing-updated") {
		t.Fatalf("conn1 should receive, got type=%s", msg1.Type)
	}
	// conn2 should not have a message
	select {
	case msg := <-conn2.send:
		t.Fatalf("conn2 should not receive, got %+v", msg)
	default:
	}
}

func TestBroadcastTo_CollectionChannel(t *testing.T) {
	h := &Handler{
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}

	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{"collection:0xABC": {}}
	h.mu.Lock()
	h.conns["c1"] = conn
	h.mu.Unlock()

	// Event for collection 0xABC — should match.
	h.BroadcastTo(sse.Event{Type: "listing-updated", Data: map[string]interface{}{
		"collection": "0xABC",
		"token_id":   "5",
	}})
	msg := recvWS(t, conn)
	if msg.Type != MessageType("listing-updated") {
		t.Fatalf("should receive for matching collection, got type=%s", msg.Type)
	}

	// Event for collection 0xDEF — should NOT match.
	h.BroadcastTo(sse.Event{Type: "listing-updated", Data: map[string]interface{}{
		"collection": "0xDEF",
		"token_id":   "5",
	}})
	select {
	case msg := <-conn.send:
		t.Fatalf("should not receive for non-matching collection, got %+v", msg)
	default:
	}
}

// ── serverTimeMs ────────────────────────────────────────────────────────────

func TestServerTimeMs_Func(t *testing.T) {
	h := &Handler{serverTime: func() int64 { return 12345 }}
	if n := h.serverTimeMs(); n != 12345 {
		t.Fatalf("serverTimeMs = %d, want 12345", n)
	}
}

func TestServerTimeMs_Fallback(t *testing.T) {
	h := &Handler{serverTime: nil}
	now := h.serverTimeMs()
	if now <= 0 {
		t.Fatal("expected positive server time")
	}
}

func TestOriginAllowed(t *testing.T) {
	cfg := &config.Config{FrontendURL: "https://magicwebb.fly.dev"}

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin", "magicwebb.fly.dev", "", true},
		{"same origin", "magicwebb.fly.dev", "https://magicwebb.fly.dev", true},
		{"configured frontend", "internal.fly.dev", "https://magicwebb.fly.dev", true},
		{"evil origin", "magicwebb.fly.dev", "https://evil.example", false},
		{"malformed", "magicwebb.fly.dev", "://bad", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ctx fasthttp.RequestCtx
			ctx.Request.SetHost(tt.host)
			if tt.origin != "" {
				ctx.Request.Header.Set("Origin", tt.origin)
			}
			if got := originAllowed(&ctx, cfg); got != tt.want {
				t.Fatalf("originAllowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripAddrPort(t *testing.T) {
	tests := map[string]string{
		"192.0.2.1:443":       "192.0.2.1",
		"192.0.2.1":           "192.0.2.1",
		"[2001:db8::1]:443":   "2001:db8::1",
		"2001:db8::1":         "2001:db8::1",
		"example.com:notport": "example.com:notport",
	}
	for in, want := range tests {
		if got := stripAddrPort(in); got != want {
			t.Fatalf("stripAddrPort(%q) = %q, want %q", in, got, want)
		}
	}
}
