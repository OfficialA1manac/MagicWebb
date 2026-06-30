// Package ws provides bidirectional WebSocket real-time communication.
// It extends the SSE broadcaster with client-to-server messaging capabilities
// while reusing the same push infrastructure for server-to-client events.
package ws

import (
	"encoding/json"
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
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// FastHTTP upgrader — works directly with fasthttp.RequestCtx from Fiber.
var upgrader = websocket.FastHTTPUpgrader{
	CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		return true // CSP + CORS middleware already enforce origin at the Fiber level
	},
}

// Per-IP connection limits (same pattern as SSE handler).
const wsPerIPLimit = 20
const wsGlobalLimit = 5_000

// Connection represents a single authenticated WebSocket connection.
type Connection struct {
	id     string
	conn   *websocket.Conn
	addr   string // wallet address from JWT ("" for unauthenticated)
	ip     string // client IP for rate limiting
	send   chan []byte
	done   chan struct{}
	once   sync.Once
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
			// Future: dispatch actions (bid, accept, etc.) to handler functions.
			// For now, acknowledge the action was received.
			c.writeJSON(Message{Type: MsgAck, Data: mustJSON(AckData{
				Status:  "ok",
				Message: "action received: " + act.Action,
			})})

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
	cfg          *config.Config
	bcast        *sse.Broadcaster
	serverTime   func() int64
	mu           sync.RWMutex
	conns        map[string]*Connection // id → Connection
	ipCounters   map[string]*int64      // ip → atomic counter
}

// NewHandler creates a WebSocket Handler.
// serverTime is a function that returns the latest block timestamp in ms (from indexer).
func NewHandler(cfg *config.Config, bcast *sse.Broadcaster, serverTime func() int64) *Handler {
	return &Handler{
		cfg:        cfg,
		bcast:      bcast,
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
// subscribes to the SSE broadcaster for push events, and manages the read/write
// lifecycle. Returns 429 when the per-IP or global connection limit is exceeded.
func (h *Handler) HandleWebSocket(c *fiber.Ctx) error {
	ip := c.IP()

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
		// envelope, and forward to the WebSocket connection.
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
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

// BroadcastTo sends an event to all connected WebSocket clients.
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


