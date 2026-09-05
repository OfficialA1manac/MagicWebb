package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
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

// ── METRICS_TOKEN gate ─────────────────────────────────────────────────────
//
// metricsAuth is the middleware in front of /internal/metrics. The contract:
//   * METRICS_TOKEN unset  → public, exactly the pre-gate behaviour (all three
//     Fly apps scrape unauthenticated today — the gate must not break them).
//   * METRICS_TOKEN set    → Authorization: Bearer <token> or
//     X-Metrics-Token: <token> passes; anything else is a bare 404 so the
//     endpoint is indistinguishable from an unknown path.
//
// The tests exercise metricsAuth with a stub terminal handler so none of the
// handler's nil dependencies (rpcpool, WS stats) are touched.

func newMetricsAuthApp() *fiber.App {
	app := fiber.New()
	app.Get("/internal/metrics", metricsAuth, func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestMetricsAuth_NoTokenConfigured_Public(t *testing.T) {
	old := config.C.MetricsToken
	config.C.MetricsToken = ""
	t.Cleanup(func() { config.C.MetricsToken = old })

	req := httptest.NewRequest(fiber.MethodGet, "/internal/metrics", nil)
	resp, err := newMetricsAuthApp().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("unset METRICS_TOKEN must keep the endpoint public; got %d", resp.StatusCode)
	}
}

func TestMetricsAuth_TokenConfigured(t *testing.T) {
	old := config.C.MetricsToken
	config.C.MetricsToken = "s3cret"
	t.Cleanup(func() { config.C.MetricsToken = old })

	cases := []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"no header", "", "", fiber.StatusNotFound},
		{"bearer ok", "Authorization", "Bearer s3cret", fiber.StatusOK},
		{"bearer wrong", "Authorization", "Bearer nope", fiber.StatusNotFound},
		{"bearer missing scheme", "Authorization", "s3cret", fiber.StatusNotFound},
		{"x-metrics-token ok", "X-Metrics-Token", "s3cret", fiber.StatusOK},
		{"x-metrics-token wrong", "X-Metrics-Token", "nope", fiber.StatusNotFound},
	}
	app := newMetricsAuthApp()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodGet, "/internal/metrics", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("%s: got %d, want %d", tc.name, resp.StatusCode, tc.want)
			}
		})
	}
}
