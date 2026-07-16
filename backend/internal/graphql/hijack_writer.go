// Package graphql — GQL-4: Hijack-aware response writer for WebSocket bridge.
//
// gqlgen's transport.Websocket requires the underlying http.ResponseWriter to
// implement http.Hijacker so gorilla/websocket.Upgrader can hijack the raw
// net.Conn. Fiber's fasthttp doesn't expose Hijacker through fasthttpadaptor,
// so this writer wraps a net.Conn obtained via fasthttp.RequestCtx.Hijack()
// and provides the http.Hijacker interface directly.
package graphql

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// hijackWriter implements http.ResponseWriter and http.Hijacker using a raw
// net.Conn obtained from fasthttp.RequestCtx.Hijack(). It is NOT safe for
// concurrent use — the gqlgen WS transport uses it sequentially within a
// single goroutine per connection.
type hijackWriter struct {
	conn   net.Conn
	buf    *bufio.ReadWriter
	header http.Header
	wroteHeader bool
	mu     sync.Mutex
}

// newHijackWriter creates a response writer backed by a raw net.Conn.
// The bufio.ReadWriter wraps the conn for buffered I/O during the
// WebSocket handshake (HTTP response headers + 101 Switching Protocols).
func newHijackWriter(conn net.Conn) *hijackWriter {
	return &hijackWriter{
		conn:   conn,
		buf:    bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
		header: make(http.Header),
	}
}

func (w *hijackWriter) Header() http.Header {
	return w.header
}

func (w *hijackWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wroteHeader {
		w.writeHeaderLocked(http.StatusOK)
	}
	return w.buf.Write(b)
}

func (w *hijackWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wroteHeader {
		return // duplicate calls are no-ops per http.ResponseWriter contract
	}
	w.writeHeaderLocked(statusCode)
}

func (w *hijackWriter) writeHeaderLocked(statusCode int) {
	w.wroteHeader = true
	// Write status line.
	fmt.Fprintf(w.buf, "HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode))
	// Write headers.
	for key, vals := range w.header {
		for _, v := range vals {
			fmt.Fprintf(w.buf, "%s: %s\r\n", key, v)
		}
	}
	w.buf.WriteString("\r\n")
	w.buf.Flush()
}

// Hijack implements http.Hijacker. Returns the raw net.Conn and the
// buffered read/writer so gqlgen's WebSocket transport can take over
// the connection after the HTTP handshake.
func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Flush any buffered response data before handing off.
	if w.wroteHeader {
		w.buf.Flush()
	}
	return w.conn, w.buf, nil
}

// Flush implements http.Flusher. Called by gqlgen after writing response
// headers to ensure they're sent before the WebSocket handshake completes.
func (w *hijackWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf != nil && w.buf.Writer != nil {
		w.buf.Flush()
	}
}

// Compile-time interface checks.
var _ http.ResponseWriter = (*hijackWriter)(nil)
var _ http.Hijacker = (*hijackWriter)(nil)
var _ http.Flusher = (*hijackWriter)(nil)
