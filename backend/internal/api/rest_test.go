package api

// SSE-specific tests have been removed alongside the deprecated /events endpoint.
// The SSE handler (sseHandler) was replaced by WebSocket-based push events.
// WebSocket handler tests live in internal/ws/handler_test.go.
//
// Add new REST endpoint integration tests here as API routes evolve.
