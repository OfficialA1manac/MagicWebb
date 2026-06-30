// Package ws provides bidirectional WebSocket real-time communication.
// It extends the SSE broadcaster with client-to-server messaging capabilities
// while reusing the same push infrastructure for server-to-client events.
package ws

import "encoding/json"

// MessageType enumerates the kinds of messages exchanged over WebSocket.
type MessageType string

const (
	// Server-to-client event types (mirror SSE event types).
	MsgListingUpdated   MessageType = "listing-updated"
	MsgAuctionUpdated   MessageType = "auction-updated"
	MsgOfferUpdated     MessageType = "offer-updated"
	MsgNotification     MessageType = "notification"
	MsgActivity         MessageType = "activity"

	// Client-to-server request types.
	MsgPing   MessageType = "ping"
	MsgAction MessageType = "action"

	// Server-to-client response types.
	MsgPong   MessageType = "pong"
	MsgAck    MessageType = "ack"
	MsgError  MessageType = "error"
)

// Message is the JSON envelope for all WebSocket communication.
type Message struct {
	Type MessageType     `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// PingData is sent by the client to keep the connection alive.
type PingData struct{}

// PongData is sent by the server in response to a ping.
type PongData struct {
	ServerTimeMs int64 `json:"server_time_ms"`
}

// ActionData is a client-initiated action (future: bid, accept, etc.).
type ActionData struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

// AckData is sent by the server to acknowledge a client message.
type AckData struct {
	Status  string `json:"status"` // "ok" | "error"
	Message string `json:"message,omitempty"`
}
