package main

import (
	"testing"

	"github.com/gofiber/fiber/v2"
)

// The Prometheus exposition endpoint and the human-facing HTML dashboard were
// both registered on "/metrics". Fiber serves whichever route was registered
// first, and registerMetricsRoute runs long before mountUI — so every "Metrics"
// link in both frontends rendered raw exposition text and the dashboard was
// unreachable in production. Nothing caught it: neither path had a test.
//
// These tests pin the split. The handler is never invoked, so nil dependencies
// are fine — only the registered paths are under test.

func TestRegisterMetricsRoute_UsesInternalPath(t *testing.T) {
	app := fiber.New()
	registerMetricsRoute(app, nil, nil, nil, nil)

	var found bool
	for _, r := range app.GetRoutes() {
		if r.Method == fiber.MethodGet && r.Path == "/internal/metrics" {
			found = true
		}
	}
	if !found {
		t.Fatal("Prometheus endpoint not registered at /internal/metrics")
	}
}

func TestRegisterMetricsRoute_DoesNotClaimMetrics(t *testing.T) {
	app := fiber.New()
	registerMetricsRoute(app, nil, nil, nil, nil)

	for _, r := range app.GetRoutes() {
		if r.Method == fiber.MethodGet && r.Path == "/metrics" {
			t.Fatal("Prometheus endpoint registered on /metrics — that path belongs " +
				"to the HTML dashboard (uiMetrics); registering both shadows the page")
		}
	}
}
