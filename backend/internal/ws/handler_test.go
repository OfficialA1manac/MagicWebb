package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// ── extractSSEEventType ─────────────────────────────────────────────────────

func TestExtractSSEEventType_Normal(t *testing.T) {
	sse := "event: listing-updated\ndata: {\"x\":1}\n\n"
	got := extractSSEEventType(sse)
	if got != "listing-updated" {
		t.Fatalf("extractSSEEventType = %q, want %q", got, "listing-updated")
	}
}

func TestExtractSSEEventType_CRLF(t *testing.T) {
	sse := "event: auction-updated\r\ndata: {\"x\":1}\r\n\r\n"
	got := extractSSEEventType(sse)
	if got != "auction-updated" {
		t.Fatalf("extractSSEEventType = %q, want %q", got, "auction-updated")
	}
}

func TestExtractSSEEventType_NoEventType(t *testing.T) {
	sse := "data: {\"x\":1}\n\n"
	got := extractSSEEventType(sse)
	if got != "" {
		t.Fatalf("extractSSEEventType = %q, want \"\"", got)
	}
}

func TestExtractSSEEventType_ExtraFields(t *testing.T) {
	sse := "event: activity\ndata: {\"x\":1}\nid: 42\n\n"
	got := extractSSEEventType(sse)
	if got != "activity" {
		t.Fatalf("extractSSEEventType = %q, want %q", got, "activity")
	}
}

// ── sseToWSMessage ──────────────────────────────────────────────────────────

func TestSSEToWSMessage_Valid(t *testing.T) {
	input := "event: listing-updated\ndata: {\"collection\":\"0xabc\",\"token_id\":\"1\"}\n\n"
	raw := sseToWSMessage(input)
	if raw == nil {
		t.Fatal("sseToWSMessage returned nil")
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MsgListingUpdated {
		t.Fatalf("type = %s, want %s", msg.Type, MsgListingUpdated)
	}
	var payload map[string]string
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if payload["collection"] != "0xabc" || payload["token_id"] != "1" {
		t.Fatalf("unexpected data: %+v", payload)
	}
}

func TestSSEToWSMessage_CRLF(t *testing.T) {
	input := "event: auction-updated\r\ndata: {\"id\":42}\r\n\r\n"
	raw := sseToWSMessage(input)
	if raw == nil {
		t.Fatal("sseToWSMessage returned nil")
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MsgAuctionUpdated {
		t.Fatalf("type = %s, want %s", msg.Type, MsgAuctionUpdated)
	}
}

func TestSSEToWSMessage_Empty(t *testing.T) {
	if got := sseToWSMessage(""); got != nil {
		t.Fatal("expected nil for empty input")
	}
	if got := sseToWSMessage("\n\n"); got != nil {
		t.Fatal("expected nil for blank input")
	}
}

func TestSSEToWSMessage_EventOnly(t *testing.T) {
	input := "event: ping\n\n"
	raw := sseToWSMessage(input)
	if raw == nil {
		t.Fatal("sseToWSMessage returned nil")
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MessageType("ping") {
		t.Fatalf("type = %s, want ping", msg.Type)
	}
}

// ── splitLines ──────────────────────────────────────────────────────────────

func TestSplitLines_LF(t *testing.T) {
	lines := splitLines("a\nb\nc")
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Fatalf("splitLines = %v, want [a b c]", lines)
	}
}

func TestSplitLines_CRLF(t *testing.T) {
	lines := splitLines("a\r\nb\r\nc")
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Fatalf("splitLines = %v, want [a b c]", lines)
	}
}

func TestSplitLines_Mixed(t *testing.T) {
	lines := splitLines("a\r\nb\nc\r\n")
	if len(lines) != 4 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Fatalf("splitLines = %v, want [a b c]", lines)
	}
}

// ── dispatchAction ──────────────────────────────────────────────────────────

func TestDispatchAction_NilQ(t *testing.T) {
	h := &Handler{q: nil}
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
	h := &Handler{q: db.New(nil)} // q is non-nil but pool is nil — fine for this test
	conn := newTestConn()
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
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xabc", "1").
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri",
		}).AddRow(
			"0xabc", "1", "0xseller", "1000000000000000000", int64(1),
			"erc721", now.Add(24*time.Hour), now, "0xtx",
			"MyToken", "https://example.com/img.png",
		))

	q := db.New(mock)
	h := &Handler{q: q}
	conn := newTestConn()
	h.handleGetListing(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleGetListing_NotFound(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xabc", "1").
		WillReturnError(pgx.ErrNoRows)

	q := db.New(mock)
	h := &Handler{q: q}
	conn := newTestConn()
	h.handleGetListing(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgError {
		t.Fatalf("type = %s, want error", msg.Type)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(42)).
		WillReturnRows(pgxmock.NewRows([]string{
			"auction_id", "collection", "token_id", "seller", "standard",
			"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
			"starts_at", "ends_at", "status", "create_tx", "name", "image_uri",
		}).AddRow(
			int64(42), "0xcol", "1", "0xseller", "erc721",
			"5000000000000000000", "6000000000000000000", "0xbidder", int16(100),
			now, now.Add(24*time.Hour), "active", "0xtx", "Auction 42", "",
		))

	q := db.New(mock)
	h := &Handler{q: q}
	conn := newTestConn()
	h.handleGetAuction(conn, json.RawMessage(`{"auction_id":42}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text, principal_wei::text`).
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{
			"offer_id", "bidder", "collection", "token_id",
			"principal_wei", "fee_wei", "units", "standard",
			"expires_at", "status", "make_tx", "created_at",
		}).AddRow(
			"42", "0xbidder", "0xcol", "1",
			"1000000000000000000", "10000000000000000", int64(1), "erc721",
			now.Add(7*24*time.Hour), "pending", "0xmtx", now,
		))

	q := db.New(mock)
	h := &Handler{q: q}
	conn := newTestConn()
	h.handleGetOffer(conn, json.RawMessage(`{"offer_id":"42"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT COALESCE\(m\.name, t\.name, ''\)`).
		WithArgs("0xabc", "1").
		WillReturnRows(pgxmock.NewRows([]string{
			"name", "description", "image_uri", "animation_uri", "metadata_uri", "fetched_at",
		}).AddRow(
			"My Token", "A cool token", "https://img.com/1.png", "", "https://meta.com/1.json", now,
		))

	q := db.New(mock)
	h := &Handler{q: q}
	conn := newTestConn()
	h.handleGetToken(conn, json.RawMessage(`{"collection":"0xabc","token_id":"1"}`))

	msg := recvWS(t, conn)
	if msg.Type != MsgState {
		t.Fatalf("type = %s, want state", msg.Type)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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

	h.BroadcastTo(sse.Event{Type: "test-event", Data: map[string]string{"msg": "hello"}})

	// Both connections should receive the message
	for _, conn := range []*Connection{conn1, conn2} {
		msg := recvWS(t, conn)
		if msg.Type != MessageType("test-event") {
			t.Fatalf("type = %s, want test-event", msg.Type)
		}
		var payload map[string]string
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload["msg"] != "hello" {
			t.Fatalf("unexpected data: %+v", payload)
		}
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
