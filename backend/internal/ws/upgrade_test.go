package ws

import (
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// This test exists because the handshake cannot be exercised any other way.
//
// fasthttp completes a websocket upgrade by registering a callback through
// ctx.Hijack and invoking it only after the Fiber handler has returned. A unit
// test that calls HandleWebSocket directly therefore observes a "successful"
// upgrade no matter what the handler does afterwards — which is exactly how the
// handler shipped for months writing a 101 and then overwriting it with a 400,
// rejecting every client in production while every test stayed green.
//
// Only a real listener with a real dialer can tell the difference.
func TestWebSocketUpgradeSucceeds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	bcast := sse.New()
	defer bcast.Shutdown()

	h := NewHandler(&config.Config{}, bcast, nil, nil, func() int64 { return 0 })

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ws", h.HandleWebSocket)
	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	u := url.URL{Scheme: "ws", Host: ln.Addr().String(), Path: "/ws"}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	var conn *websocket.Conn
	var resp error
	// The listener may not be accepting on the first attempt.
	for i := 0; i < 20; i++ {
		conn, _, resp = dialer.Dial(u.String(), nil)
		if resp == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp != nil {
		t.Fatalf("dial /ws: %v (a 400 here means the handler answered its own 101 with an error)", resp)
	}
	defer conn.Close()

	// The server greets every accepted connection. Receiving it proves the
	// hijacked connection is live, not merely that the status line said 101.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if len(msg) == 0 {
		t.Fatal("welcome frame was empty")
	}
	t.Logf("welcome: %s", msg)
}

// A cross-origin handshake must be refused, and refused at the HTTP layer
// rather than by accepting the socket and closing it afterwards.
func TestWebSocketUpgradeRejectsForeignOrigin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	bcast := sse.New()
	defer bcast.Shutdown()

	h := NewHandler(&config.Config{FrontendURL: "https://magicwebb.fly.dev"}, bcast, nil, nil, func() int64 { return 0 })

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ws", h.HandleWebSocket)
	go func() { _ = app.Listener(ln) }()
	defer func() { _ = app.Shutdown() }()

	u := url.URL{Scheme: "ws", Host: ln.Addr().String(), Path: "/ws"}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	var httpResp = 0
	for i := 0; i < 20; i++ {
		conn, r, derr := dialer.Dial(u.String(), map[string][]string{"Origin": {"https://evil.example"}})
		if conn != nil {
			conn.Close()
			t.Fatal("cross-origin handshake was accepted")
		}
		if r != nil {
			httpResp = r.StatusCode
			break
		}
		if derr != nil && i == 19 {
			t.Fatalf("dial: %v", derr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if httpResp != 403 {
		t.Fatalf("status = %d, want 403", httpResp)
	}
}
