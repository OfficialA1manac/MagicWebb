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

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Per-IP connection limits (same pattern as SSE handler).
const wsPerIPLimit = 20
const wsGlobalLimit = 5_000

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

	c.conn.SetReadLimit(4096) // 4 KB max per message
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

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// ———————————————————————————————————————

// Handler manages WebSocket connections and bridges them with the SSE broadcaster.
type Handler struct {
	cfg        *config.Config
	bcast      *sse.Broadcaster
	q          *db.Q
	serverTime func() int64
	mu         sync.RWMutex
	conns      map[string]*Connection // id → Connection
	ipCounters map[string]*int64      // ip → atomic counter
}

// NewHandler creates a WebSocket Handler.
// serverTime is a function that returns the latest block timestamp in ms (from indexer).
func NewHandler(cfg *config.Config, bcast *sse.Broadcaster, q *db.Q, serverTime func() int64) *Handler {
	return &Handler{
		cfg:        cfg,
		bcast:      bcast,
		q:          q,
		serverTime: serverTime,
		conns:      make(map[string]*Connection),
		ipCounters: make(map[string]*int64),
	}
}

// extractSSEEventType parses an SSE-formatted string and returns the event
// type (the value after "event:"). Returns "" when no event type is found.
// SSE format: "event: TYPE\ndata: JSON\n\n"
func extractSSEEventType(sse string) string {
	s := strings.ReplaceAll(sse, "\r\n", "\n")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event:") {
			return strings.TrimSpace(line[6:])
		}
	}
	return ""
}

func (h *Handler) serverTimeMs() int64 {
	if h.serverTime != nil {
		return h.serverTime()
	}
	return time.Now().UnixMilli()
}

// HandleWebSocket is the Fiber handler for GET /ws.
// It upgrades the HTTP connection to WebSocket, authenticates via JWT cookie,
// subscribes to the SSE broadcaster for push events, and manages the read/write
// lifecycle. Returns 429 when the per-IP or global connection limit is exceeded.
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

	// Subscribe to SSE broadcaster for push events
	ch, cancel, ok := h.bcast.Subscribe()
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

	// Write pump: forwards broadcaster events to the WebSocket
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

		// Read from broadcaster events, convert SSE format to WebSocket JSON
		// envelope, and forward to the WebSocket connection — filtered by
		// the client's channel subscriptions (if any).
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				// Extract the event type from the SSE string so we can check
				// subscription filters before unmarshalling the full payload.
				eventType := extractSSEEventType(msg)
				if eventType != "" && !conn.isSubscribedToEvent(eventType) {
					continue // skip — client not subscribed to this event type
				}
				// Convert SSE-formatted event ("event: TYPE\ndata: JSON\n\n")
				// to WebSocket JSON envelope ({"type":"TYPE","data":JSON}).
				if wsMsg := sseToWSMessage(msg); wsMsg != nil {
					select {
					case conn.send <- wsMsg:
					default:
						// Slow client — drop
					}
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

// sseToWSMessage converts an SSE-formatted broadcaster event to a WebSocket JSON
// envelope. SSE format: "event: TYPE\ndata: JSON\n\n"
// WS format: {"type":"TYPE","data":JSON}
// Returns nil when the SSE string cannot be parsed.
func sseToWSMessage(sse string) []byte {
	// Split on \n — at most 3 lines expected: event line, data line, blank
	// Handle both LF and CRLF.
	var eventType, dataRaw string
	for _, line := range splitLines(sse) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			dataRaw = strings.TrimSpace(line[5:])
		}
	}
	if eventType == "" && dataRaw == "" {
		return nil
	}

	env := Message{
		Type: MessageType(eventType),
		Data: json.RawMessage(dataRaw),
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return out
}

// splitLines splits s on \n, handling both LF and CRLF.
func splitLines(s string) []string {
	// Normalize CRLF → LF first
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// authenticate extracts the wallet address from the JWT cookie, if present.
// Returns "" for unauthenticated connections (still allowed for public data).
func (h *Handler) authenticate(c *fiber.Ctx) string {
	// Try session cookies first
	for _, name := range sessionCookieNames(c) {
		if v := c.Cookies(name); v != "" {
			if a, err := auth.Verify(v, h.cfg.JWTSecret, auth.DefaultAudience); err == nil {
				return a
			}
		}
	}
	// Try Authorization header
	if hdr := c.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		if a, err := auth.Verify(strings.TrimPrefix(hdr, "Bearer "), h.cfg.JWTSecret, auth.DefaultAudience); err == nil {
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
		if !strings.HasPrefix(p, "mw_s_") {
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
	if h.q == nil {
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
// result via WebSocket. Expected params: {"collection":"0x...","token_id":"1"}
type getListingParams struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
}

func (h *Handler) handleGetListing(c *Connection, raw json.RawMessage) {
	var p getListingParams
	if err := json.Unmarshal(raw, &p); err != nil || p.Collection == "" || p.TokenID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_listing params: need collection + token_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	row, err := h.q.GetListing(ctx, p.Collection, p.TokenID)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(row)})
}

// ctxOrBackground returns a context with a 5-second timeout so a slow/hanging
// DB query cannot stall the connection's read loop indefinitely. The caller
// MUST call cancel() when done.
func (*Connection) ctxOrBackground() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// handleGetAuction fetches an auction by ID and sends the result via WebSocket.
// Expected params: {"auction_id":123}
type getAuctionParams struct {
	AuctionID int64 `json:"auction_id"`
}

func (h *Handler) handleGetAuction(c *Connection, raw json.RawMessage) {
	var p getAuctionParams
	if err := json.Unmarshal(raw, &p); err != nil || p.AuctionID <= 0 {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_auction params: need auction_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	row, err := h.q.GetAuction(ctx, p.AuctionID)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(row)})
}

// handleGetOffer fetches an offer by ID and sends the result via WebSocket.
// Expected params: {"offer_id":"123"}
type getOfferParams struct {
	OfferID string `json:"offer_id"`
}

func (h *Handler) handleGetOffer(c *Connection, raw json.RawMessage) {
	var p getOfferParams
	if err := json.Unmarshal(raw, &p); err != nil || p.OfferID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_offer params: need offer_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	row, err := h.q.GetOffer(ctx, p.OfferID)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(row)})
}

// handleGetToken fetches token metadata and sends the result via WebSocket.
// Expected params: {"collection":"0x...","token_id":"1"}
type getTokenParams struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
}

func (h *Handler) handleGetToken(c *Connection, raw json.RawMessage) {
	var p getTokenParams
	if err := json.Unmarshal(raw, &p); err != nil || p.Collection == "" || p.TokenID == "" {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "invalid get_token params: need collection + token_id"})})
		return
	}
	ctx, cancel := c.ctxOrBackground()
	defer cancel()
	name, desc, imageURI, animURI, metaURI, fetchedAt, err := h.q.GetTokenFullMetadata(ctx, p.Collection, p.TokenID)
	if err != nil {
		c.writeJSON(Message{Type: MsgError, Data: mustJSON(AckData{Status: "error", Message: "not found"})})
		return
	}
	resp := struct {
		Collection   string `json:"collection"`
		TokenID      string `json:"token_id"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		ImageURI     string `json:"image_uri"`
		AnimationURI string `json:"animation_uri"`
		MetadataURI  string `json:"metadata_uri"`
		FetchedAt    string `json:"fetched_at"`
	}{
		Collection:   p.Collection,
		TokenID:      p.TokenID,
		Name:         name,
		Description:  desc,
		ImageURI:     imageURI,
		AnimationURI: animURI,
		MetadataURI:  metaURI,
		FetchedAt:    fetchedAt.Format(time.RFC3339Nano),
	}
	c.writeJSON(Message{Type: MsgState, Data: mustJSON(resp)})
}

// ── Subscription management ───────────────────────────────────────────────────

// subscribe adds channels to the connection's subscription set and confirms.
// user: channels are gated on the authenticated wallet address — a connection
// cannot subscribe to another wallet's notification events. Unauthenticated
// connections (addr == "") are rejected for user: channels entirely.
//
// v1 filter granularity: channelMatchesEventType only checks the channel
// prefix (token:/collection:/user:), not the full encoded address/ID. This
// means a subscription to "token:0xAAA:1" will receive ALL token events
// across every collection/token, not just 0xAAA:1. Per-entity scoping would
// require peeking into SSE payload bodies; this coarse category-level filter
// is the intentional v1 trade-off.
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
// a given SSE event type. When the connection has no subscriptions, it
// receives all events (backward-compatible default).
func (c *Connection) isSubscribedToEvent(eventType string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	if len(c.subscriptions) == 0 {
		return true // no subscriptions → receive all
	}
	for ch := range c.subscriptions {
		if channelMatchesEventType(ch, eventType) {
			return true
		}
	}
	return false
}

// ── BroadcastTo (direct push) ────────────────────────────────────────────────

// BroadcastTo sends an event to ALL connected WebSocket clients (no subscription
// filtering). It is used for direct server-side pushes that bypass the SSE
// broadcaster. Clients with no active subscriptions receive all events;
// clients with active subscriptions also receive all BroadcastTo events
// (subscription filtering only applies to the SSE bridge goroutine, which
// reads from the broadcaster's shared event channel).
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
