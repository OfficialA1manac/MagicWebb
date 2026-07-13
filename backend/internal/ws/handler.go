// Package ws provides bidirectional WebSocket real-time communication.
// It extends the SSE broadcaster with client-to-server messaging capabilities
// while reusing the same push infrastructure for server-to-client events.
package ws

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	marketplacev1 "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	marketplacev1connect "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Per-IP connection limits (same pattern as SSE handler).
const wsPerIPLimit = 20
const wsGlobalLimit = 5_000

// Per-connection rate limiting: max messages per second from a single client.
// Token bucket: 20 tokens, refilled at 10/sec — allows bursts of up to 20
// messages then settles at 10 msg/s steady state. A malicious client spamming
// MsgAction with malformed params would otherwise force JSON-unmarshal on
// every message without any backpressure.
const wsConnMsgLimit = 20   // burst capacity
const wsConnMsgRefill = 10  // tokens per second

// Connection represents a single authenticated WebSocket connection.
type Connection struct {
	id            string
	conn          *websocket.Conn
	addr          string // wallet address from JWT ("" for unauthenticated)
	ip            string // client IP for rate limiting
	send          chan []byte
	done          chan struct{}
	once          sync.Once
	subscriptions map[string]struct{} // set of subscribed channels (guarded by subMu)
	subMu         sync.RWMutex        // guards subscriptions against concurrent read/write

	// Per-connection token bucket for client message rate limiting.
	// tokens available for immediate consumption (atomic; consumed by readPump).
	// Refilled by a background goroutine at wsConnMsgRefill/sec up to wsConnMsgLimit.
	msgTokens   int64
}

// writePump sends messages from the broadcaster to the WebSocket connection.
func (c *Connection) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer func() {
		c.once.Do(func() { close(c.done) })
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			// readPump exited first (client disconnected); stop writing.
			return
		}
	}
}

// readPump reads messages from the WebSocket connection and dispatches them.
func (c *Connection) readPump(h *Handler) {
	defer func() {
		c.once.Do(func() { close(c.done) })
	}()

	// Start per-connection token bucket refill. A dedicated goroutine adds
	// wsConnMsgRefill tokens per second up to wsConnMsgLimit cap. The refill
	// stops when the connection closes (done channel closes).
	c.msgTokens = wsConnMsgLimit // start with a full bucket
	go c.refillTokens()

	c.conn.SetReadLimit(16384) // 16 KB max per message (was 4096 — too small for state payloads)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Per-connection rate limiting: token bucket gates ALL client→server
		// messages. 20 msg burst, 10 msg/s steady state. A malicious client
		// spamming MsgAction with malformed params used to force JSON-unmarshal
		// on every message without backpressure.
		if !c.consumeMsgToken() {
			c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "rate limit exceeded"})})
			continue
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "malformed message"})})
			continue
		}

		switch msg.Type {
		case MsgPing:
			c.writeJSON(Message{Type: MsgPong, Data: mustJSON(PongData{ServerTimeMs: h.serverTimeMs()})})

		case MsgAction:
			var act ActionData
			if err := json.Unmarshal(msg.Data, &act); err != nil {
				c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid action"})})
				continue
			}
			h.dispatchAction(c, act)

		case MsgSubscribe:
			var sub SubscribeData
			if err := json.Unmarshal(msg.Data, &sub); err != nil {
				c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid subscribe payload"})})
				continue
			}
			c.subscribe(sub.Channels)

		case MsgUnsubscribe:
			var unsub UnsubscribeData
			if err := json.Unmarshal(msg.Data, &unsub); err != nil {
				c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid unsubscribe payload"})})
				continue
			}
			c.unsubscribe(unsub.Channels)

		default:
			c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{
				Status:  "error",
				Message: "unknown message type: " + string(msg.Type),
			})})
		}
	}
}

func (c *Connection) writeJSON(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		// Slow client — drop the message
	}
}

// consumeMsgToken attempts to consume one token from the per-connection
// token bucket. Returns false when the bucket is empty (rate limit exceeded).
func (c *Connection) consumeMsgToken() bool {
	for {
		current := atomic.LoadInt64(&c.msgTokens)
		if current <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt64(&c.msgTokens, current, current-1) {
			return true
		}
	}
}

// refillTokens adds wsConnMsgRefill tokens per second to the bucket up to
// wsConnMsgLimit. Runs until the connection closes (done channel fired).
func (c *Connection) refillTokens() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			for {
				current := atomic.LoadInt64(&c.msgTokens)
				next := current + wsConnMsgRefill
				if next > wsConnMsgLimit {
					next = wsConnMsgLimit
				}
				if atomic.CompareAndSwapInt64(&c.msgTokens, current, next) {
					break
				}
			}
		}
	}
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// ———————————————————————————————————————

// Handler manages WebSocket connections and bridges them with the SSE broadcaster.
type Handler struct {
	cfg         *config.Config
	bcast       *sse.Broadcaster
	q           *db.Q
	client      marketplacev1connect.MarketplaceServiceClient
	serverTime  func() int64
	mu          sync.RWMutex
	conns       map[string]*Connection // id → Connection
	ipCounters  map[string]*int64      // ip → atomic counter
	eventsSent  atomic.Int64           // total events pushed to all WS clients
	connCount   atomic.Int64           // total connections established (lifetime)
}

// NewHandler creates a WebSocket Handler.
// serverTime is a function that returns the latest block timestamp in ms (from indexer).
// client is a Connect-RPC client for the MarketplaceService, used to serve
// action-based queries (get_listing, get_auction, get_offer, get_token).
func NewHandler(cfg *config.Config, bcast *sse.Broadcaster, q *db.Q, client marketplacev1connect.MarketplaceServiceClient, serverTime func() int64) *Handler {
	return &Handler{
		cfg:        cfg,
		bcast:      bcast,
		q:          q,
		client:     client,
		serverTime: serverTime,
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}
}

func (h *Handler) serverTimeMs() int64 {
	if h.serverTime != nil {
		return h.serverTime()
	}
	return time.Now().UnixMilli()
}

// HandleWebSocket is the Fiber handler for GET /ws.
// It upgrades the HTTP connection to WebSocket, authenticates via JWT cookie,
// subscribes to the broadcaster for push events (raw Event objects, no SSE
// formatting), and manages the read/write lifecycle.
func (h *Handler) HandleWebSocket(c *fiber.Ctx) error {
	ip := clientIP(c)

	// Per-IP connection cap
	if !h.acquireIP(ip) {
		return c.Status(fiber.StatusTooManyRequests).SendString("too many connections from this IP")
	}

	// Global connection cap
	h.mu.RLock()
	globalCount := len(h.conns)
	h.mu.RUnlock()
	if globalCount >= wsGlobalLimit {
		h.releaseIP(ip)
		return c.Status(fiber.StatusServiceUnavailable).SendString("too many subscribers")
	}

	// Extract wallet address from JWT cookie (if present)
	addr := h.authenticate(c)

	// Upgrade to WebSocket
	var wsConn *websocket.Conn
	upgrader := websocket.FastHTTPUpgrader{
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			return originAllowed(ctx, h.cfg)
		},
	}
	upgradeErr := upgrader.Upgrade(c.Context(), func(conn *websocket.Conn) {
		wsConn = conn
	})
	if upgradeErr != nil || wsConn == nil {
		h.releaseIP(ip)
		return c.Status(fiber.StatusBadRequest).SendString("websocket upgrade failed")
	}

	// Subscribe to broadcaster for raw push events (no SSE formatting).
	eventCh, cancel, ok := h.bcast.SubscribeRaw()
	if !ok {
		_ = wsConn.Close()
		h.releaseIP(ip)
		return c.Status(fiber.StatusServiceUnavailable).SendString("too many subscribers")
	}

	conn := &Connection{
		id:   uuid.New().String(),
		conn: wsConn,
		addr: addr,
		ip:   ip,
		send: make(chan []byte, 64),
		done: make(chan struct{}),
	}

	h.mu.Lock()
	h.conns[conn.id] = conn
	h.mu.Unlock()
	h.connCount.Add(1)

	// Write pump: forwards broadcaster events to the WebSocket as JSON
	// envelopes. Raw Event objects are marshalled directly — no SSE parsing
	// or conversion needed.
	go func() {
		defer func() {
			cancel()
			h.mu.Lock()
			delete(h.conns, conn.id)
			h.mu.Unlock()
			h.releaseIP(ip)
			_ = wsConn.Close()
		}()
		defer conn.once.Do(func() { close(conn.done) })

		// Send a welcome message
		welcome := Message{
			Type: MsgAck,
			Data: mustJSON(AckData{
				Status:  "ok",
				Message: "connected",
			}),
		}
		welcomeData, _ := json.Marshal(welcome)
		select {
		case conn.send <- welcomeData:
		default:
		}

		// Read raw broadcaster events and forward to WebSocket.
		// Subscription filtering is applied per-event-type so clients
		// only receive events matching their subscribed channels.
		for {
			select {
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				// Skip expensive marshalling when no subscription matches.
				// The isSubscribedToEvent method does its own coarse pre-check
				// under a single lock — we just call it directly now.
				payload, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				// Filter by client's channel subscriptions (with per-entity
				// scoping when payload is available).
				if !conn.isSubscribedToEvent(string(ev.Type), payload) {
					continue
				}
				env := Message{
					Type: MessageType(ev.Type),
					Data: json.RawMessage(payload),
				}
				msg, err := json.Marshal(env)
				if err != nil {
					continue
				}
				select {
				case conn.send <- msg:
					h.eventsSent.Add(1)
				default:
					// Slow client — drop
					// We do NOT increment eventsSent — the event was dropped
				}
			case <-conn.done:
				return
			}
		}
	}()

	// Read pump: handles client-to-server messages
	go conn.readPump(h)

	// Write pump: sends messages from the send channel to the WebSocket
	conn.writePump()

	return nil
}

func clientIP(c *fiber.Ctx) string {
	if v := strings.TrimSpace(c.Get("Fly-Client-IP")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Get("Forwarded")); v != "" {
		for _, part := range strings.Split(v, ";") {
			p := strings.TrimSpace(part)
			if !strings.HasPrefix(strings.ToLower(p), "for=") {
				continue
			}
			id := strings.Trim(p[4:], " \"")
			if id = stripAddrPort(id); id != "" {
				return id
			}
		}
	}
	if v := strings.TrimSpace(c.Get("X-Forwarded-For")); v != "" {
		if i := strings.LastIndex(v, ","); i >= 0 {
			v = strings.TrimSpace(v[i+1:])
		}
		if v != "" {
			return v
		}
	}
	return c.IP()
}

func stripAddrPort(id string) string {
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "[") {
		if end := strings.Index(id, "]"); end >= 0 {
			return id[1:end]
		}
		return id[1:]
	}
	if strings.Count(id, ":") > 1 {
		return id
	}
	if colon := strings.LastIndex(id, ":"); colon >= 0 && looksLikePort(id[colon+1:]) {
		return id[:colon]
	}
	return id
}

func looksLikePort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func originAllowed(ctx *fasthttp.RequestCtx, cfg *config.Config) bool {
	origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	if sameOriginHost(originURL.Host, strings.TrimSpace(string(ctx.Host()))) {
		return true
	}
	if cfg == nil || cfg.FrontendURL == "" {
		return false
	}
	frontendURL, err := url.Parse(cfg.FrontendURL)
	if err != nil || frontendURL.Host == "" {
		return false
	}
	return sameOriginHost(originURL.Host, frontendURL.Host) &&
		(originURL.Scheme == frontendURL.Scheme || frontendURL.Scheme == "")
}

func sameOriginHost(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

// authenticate extracts the wallet address from the JWT cookie, if present.
// Returns "" for unauthenticated connections (still allowed for public data).
func (h *Handler) authenticate(c *fiber.Ctx) string {
	// Try session cookies first (both legacy mw_s_ and new mw_a_ access tokens)
	for _, name := range sessionCookieNames(c) {
		if v := c.Cookies(name); v != "" {
			if a, err := auth.VerifyAccessToken(v, h.cfg.JWTSecret); err == nil {
				return a
			}
		}
	}
	// Try Authorization header
	if hdr := c.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		if a, err := auth.VerifyAccessToken(strings.TrimPrefix(hdr, "Bearer "), h.cfg.JWTSecret); err == nil {
			return a
		}
	}
	return ""
}

// sessionCookieNames scans cookie headers for mw_s_<addr-prefix> names.
// Mirrors the same function in api/rest.go to avoid circular imports.
func sessionCookieNames(c *fiber.Ctx) []string {
	hdr := c.Get("Cookie")
	if hdr == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, part := range strings.Split(hdr, ";") {
		p := strings.TrimSpace(part)
		if !strings.HasPrefix(p, "mw_s_") && !strings.HasPrefix(p, "mw_a_") {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		name := p[:eq]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// acquireIP increments the per-IP counter. Returns false if the limit is exceeded.
func (h *Handler) acquireIP(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	cnt, ok := h.ipCounters[ip]
	if !ok {
		cnt = new(int64)
		h.ipCounters[ip] = cnt
	}
	if atomic.AddInt64(cnt, 1) > wsPerIPLimit {
		atomic.AddInt64(cnt, -1)
		return false
	}
	return true
}

// releaseIP decrements the per-IP counter.
func (h *Handler) releaseIP(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cnt, ok := h.ipCounters[ip]; ok {
		if atomic.AddInt64(cnt, -1) <= 0 {
			delete(h.ipCounters, ip)
		}
	}
}

// ── Action dispatch ──────────────────────────────────────────────────────────

// dispatchAction routes a client action to the appropriate handler based on
// the action name. Actions are lightweight requests that don't need on-chain
// wallet signatures — they fetch current state, manage subscriptions, etc.
func (h *Handler) dispatchAction(c *Connection, act ActionData) {
	if h.client == nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "server not ready"})})
		return
	}

	switch act.Action {
	case "get_listing":
		h.handleGetListing(c, act.Params)
	case "get_auction":
		h.handleGetAuction(c, act.Params)
	case "get_offer":
		h.handleGetOffer(c, act.Params)
	case "get_token":
		h.handleGetToken(c, act.Params)
	default:
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{
			Status:  "error",
			Message: "unknown action: " + act.Action,
		})})
	}
}

// handleGetListing fetches a listing by collection + token ID and sends the
// result via WebSocket via the Connect-RPC MarketplaceService client.
func (h *Handler) handleGetListing(c *Connection, raw json.RawMessage) {
	var params struct {
		Collection string `json:"collection"`
		TokenID    string `json:"token_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Collection == "" || params.TokenID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_listing params: need collection + token_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	req := connect.NewRequest(&marketplacev1.GetListingRequest{
		Collection: params.Collection,
		TokenId:    params.TokenID,
	})
	resp, err := h.client.GetListing(ctx, req)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(resp.Msg)})
}

// ctxOrBackground returns a context with a 5-second timeout so a slow/hanging
// DB query cannot stall the connection's read loop indefinitely. The caller
// MUST call cancel() when done.
func (*Connection) ctxOrBackground() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// handleGetAuction fetches an auction by ID and sends the result via WebSocket
// via the Connect-RPC MarketplaceService client.
func (h *Handler) handleGetAuction(c *Connection, raw json.RawMessage) {
	var params struct {
		AuctionID int64 `json:"auction_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.AuctionID <= 0 {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_auction params: need auction_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	req := connect.NewRequest(&marketplacev1.GetAuctionRequest{
		AuctionId: params.AuctionID,
	})
	resp, err := h.client.GetAuction(ctx, req)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(resp.Msg)})
}

// handleGetOffer fetches an offer by ID and sends the result via WebSocket
// via the Connect-RPC MarketplaceService client.
func (h *Handler) handleGetOffer(c *Connection, raw json.RawMessage) {
	var params struct {
		OfferID string `json:"offer_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.OfferID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_offer params: need offer_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	req := connect.NewRequest(&marketplacev1.GetOfferRequest{
		OfferId: params.OfferID,
	})
	resp, err := h.client.GetOffer(ctx, req)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(resp.Msg)})
}

// handleGetToken fetches token metadata and sends the result via WebSocket
// via the Connect-RPC MarketplaceService client.
func (h *Handler) handleGetToken(c *Connection, raw json.RawMessage) {
	var params struct {
		Collection string `json:"collection"`
		TokenID    string `json:"token_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Collection == "" || params.TokenID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_token params: need collection + token_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	req := connect.NewRequest(&marketplacev1.GetTokenRequest{
		Collection: params.Collection,
		TokenId:    params.TokenID,
	})
	resp, err := h.client.GetToken(ctx, req)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(resp.Msg)})
}

// ── Subscription management ───────────────────────────────────────────────────

// subscribe adds channels to the connection's subscription set and confirms.
// user: channels are gated on the authenticated wallet address — a connection
// cannot subscribe to another wallet's notification events. Unauthenticated
// connections (addr == "") are rejected for user: channels entirely.
//
// Per-entity scoping (W5): channelMatchesEvent now parses entity IDs from
// the channel and matches against the event payload's collection/token/address
// fields. A subscription to "token:0xAAA:1" only receives events for token 0xAAA:1.
func (c *Connection) subscribe(channels []string) {
	c.subMu.Lock()
	if c.subscriptions == nil {
		c.subscriptions = make(map[string]struct{})
	}
	subscribed := make([]string, 0, len(channels))
	for _, ch := range channels {
		// Gate user: channels on the authenticated wallet address.
		if strings.HasPrefix(ch, channelUser) && strings.TrimPrefix(ch, channelUser) != c.addr {
			continue
		}
		if isValidChannel(ch) {
			c.subscriptions[ch] = struct{}{}
			subscribed = append(subscribed, ch)
		}
	}
	c.subMu.Unlock()
	c.writeJSON(Message{
		Type: MsgSubscribed,
		Data: mustJSON(SubscribedData{Channels: subscribed}),
	})
}

// unsubscribe removes channels from the connection's subscription set and confirms.
func (c *Connection) unsubscribe(channels []string) {
	c.subMu.Lock()
	unsubscribed := make([]string, 0, len(channels))
	for _, ch := range channels {
		delete(c.subscriptions, ch)
		unsubscribed = append(unsubscribed, ch)
	}
	c.subMu.Unlock()
	c.writeJSON(Message{
		Type: MsgUnsubscribed,
		Data: mustJSON(UnsubscribedData{Channels: unsubscribed}),
	})
}

// isSubscribedToEvent checks whether this connection's subscriptions match
// a given event. When the connection has no subscriptions, it receives all
// events (backward-compatible default).
//
// Does a lightweight coarse prefix check first (no JSON parsing), then parses
// the payload once for per-entity scoping (W5). Both checks happen under a
// single lock acquisition.
func (c *Connection) isSubscribedToEvent(eventType string, payloadBytes []byte) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	if len(c.subscriptions) == 0 {
		return true // no subscriptions → receive all
	}
	// Coarse prefix pre-check: does any channel match this event category?
	// Skip expensive JSON parsing if no channel would match.
	hasAnyMatch := false
	for ch := range c.subscriptions {
		if channelMatchesPrefix(ch, eventType) {
			hasAnyMatch = true
			break
		}
	}
	if !hasAnyMatch {
		return false
	}
	// Parse the event payload once (not per-channel) for efficiency.
	// If parsing fails (malformed), err on the side of delivery.
	var ev *eventPayload
	if len(payloadBytes) > 0 {
		var parsed eventPayload
		if err := json.Unmarshal(payloadBytes, &parsed); err != nil {
			return true // malformed payload → deliver
		}
		ev = &parsed
	}
	for ch := range c.subscriptions {
		if channelMatchesEvent(ch, eventType, ev) {
			return true
		}
	}
	return false
}

// ── BroadcastTo (direct push) ────────────────────────────────────────────────

// BroadcastTo sends an event to WebSocket clients, respecting per-connection
// subscription filters. Clients with no active subscriptions receive all
// events (backward-compatible default). Clients with subscriptions only
// receive events matching their subscribed channels.
// Uses the WebSocket JSON envelope format ({"type":"...","data":...})
// instead of SSE line format, so ws.js clients can parse directly.
func (h *Handler) BroadcastTo(ev sse.Event) {
	payload, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}
	env := Message{
		Type: MessageType(ev.Type),
		Data: json.RawMessage(payload),
	}
	msg, err := json.Marshal(env)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, conn := range h.conns {
		// Respect per-connection subscription filters (same logic as the
		// broadcaster event pump goroutine).
		if !conn.isSubscribedToEvent(string(ev.Type), payload) {
			continue
		}
		select {
		case conn.send <- msg:
		default:
		}
	}
}

// ActiveConns returns the current number of active WebSocket connections.
func (h *Handler) ActiveConns() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// TotalSubscriptions returns the total number of channel subscriptions across
// all connected clients. Used by the metrics dashboard to show subscription load.
func (h *Handler) TotalSubscriptions() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, conn := range h.conns {
		conn.subMu.RLock()
		total += len(conn.subscriptions)
		conn.subMu.RUnlock()
	}
	return total
}

// EventsSent returns the total number of push events delivered to all WS
// clients since the process started. Monotonic counter (never resets).
func (h *Handler) EventsSent() int64 {
	return h.eventsSent.Load()
}

// TotalConns returns the total number of WebSocket connections established
// since the process started (including those that have since disconnected).
func (h *Handler) TotalConns() int64 {
	return h.connCount.Load()
}
