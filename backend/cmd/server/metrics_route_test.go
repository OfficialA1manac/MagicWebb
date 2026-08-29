package main

import (
	"testing"

	"github.com/gofiber/fiber/v2"
)

// The Prometheus exposition endpoint and the human-facing HTML dashboard were
// both registered on "/metrics". Fiber serves whichever route was registered
// first, and registerMetricsRoute runs long before the UI mount — so every
// "Metrics" link in the site rendered raw exposition text and the dashboard
// was unreachable in production. Nothing caught it: neither path had a test.
//
// Today /metrics is the Astro-built dashboard page (app/src/pages/metrics)
// served by the mountAstro static handler; the exposition endpoint lives at
// /internal/metrics. These tests pin the Prometheus half of that split: the
// exposition endpoint stays on /internal/metrics and never claims /metrics.
// The dashboard half is a static file, not a route, so it cannot be asserted
// from app.GetRoutes() here.
//
// The handler is never invoked, so nil dependencies are fine — only the
// registered paths are under test.

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
				"to the Astro HTML dashboard; registering a route there shadows the page")
		}
	}
}
