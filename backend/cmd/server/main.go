// MagicWebb — single binary: Fiber HTTP server + blockchain indexer goroutine.
// Run: go run ./cmd/server   (no Docker, no Redis, no separate processes)
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/getsentry/sentry-go"
	sentryfiber "github.com/getsentry/sentry-go/fiber"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/health"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/keeper"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/nonce"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/rpcpool"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/webhook"
	"github.com/OfficialA1manac/MagicWebb/frontend"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	config.Load()

	if config.C.Env != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	// ── Sentry error reporting (optional — disabled when SENTRY_DSN is empty) ──
	if config.C.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              config.C.SentryDSN,
			Environment:      config.C.Env,
			EnableTracing:    true,
			TracesSampleRate: 0.1, // capture 10% of transactions to control cost
		}); err != nil {
			log.Warn().Err(err).Msg("sentry: init failed")
		} else {
			log.Info().Msg("sentry: error reporting enabled")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DB
	if err := db.Migrate(config.C.PostgresURL); err != nil {
		log.Fatal().Err(err).Msg("db migration failed")
	}
	pool, err := db.Connect(ctx, config.C.PostgresURL)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()

	// Verify deployment config matches env vars. Prevents the indexer from
	// mixing events from old and new contract instances after a redeploy.
	// Creates the initial row on first deploy; refuses to start on mismatch.
	if err := verifyDeploymentConfig(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("deployment config mismatch — contract addresses changed since last deploy; truncate on-chain data first")
	}

	// Optional read-replica pool for query offloading. When READ_POOL_URL is
	// set (e.g. a Neon read-only branch endpoint), read-heavy API queries
	// (listings, auctions, activity, search) are routed through the replica
	// pool while writes always use the primary. Nil is safe — reader() falls
	// back to the primary pool when no replica is configured.
	readPool, err := db.ConnectReadReplica(ctx, config.C.ReadPoolURL)
	if err != nil {
		log.Fatal().Err(err).Msg("read replica connect failed")
	}
	if readPool != nil {
		defer readPool.Close()
	}
	q := db.New(pool)
	if readPool != nil {
		q = q.WithReadReplica(readPool)
	}

	// SSE broadcaster with cross-instance fan-out via gRPC streaming mesh.
	// Degrades to local-only delivery if the listen conn is unavailable.
	bcast := sse.NewGrpcBridged(ctx, config.C.GRPCPort, config.C.GRPCPeers)

	// Shared (Postgres) rate limiter + nonce store, so limits and single-use
	// SIWE nonces hold across instances.
	rl := ratelimit.NewPg(pool)
	ns := nonce.NewPg(pool)
	rs := auth.NewPgRefreshStore(pool) // refresh token family rotation
	al := auth.NewPgAuditLogger(pool)  // async auth audit log
	aks := auth.NewPgAPIKeyStore(pool) // AUTH-3: API key store for machine-to-machine auth

	// ── OpenTelemetry tracing (optional — disabled when endpoint is empty) ──
	var tp *sdktrace.TracerProvider
	if config.C.OTELExporterOTLPEndpoint != "" {
		expCtx, expCancel := context.WithTimeout(ctx, 5*time.Second)
		exp, err := otlptracegrpc.New(expCtx,
			otlptracegrpc.WithEndpoint(config.C.OTELExporterOTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		expCancel()
		if err != nil {
			log.Warn().Err(err).Msg("otel: exporter init failed")
		} else {
			tp = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
			otel.SetTracerProvider(tp)
			// Set global propagator so trace context is propagated via
			// W3C TraceContext headers (traceparent, tracestate).
			otel.SetTextMapPropagator(propagation.TraceContext{})
			log.Info().Msg("otel: tracing enabled")
		}
	}

	// Ethereum access: rotation + failover across every configured endpoint
	// (RPC_URLS, falling back to RPC_URL). All indexer/keeper reads, writes and
	// log filters go through the pool.
	eth, err := rpcpool.New(ctx, config.C.RPCURLs, rpcpool.DefaultTimeout)
	if err != nil {
		log.Fatal().Err(err).Msg("eth rpc pool init failed")
	}

	// RPC-1: Wire RPC pool health events to the SSE broadcaster so WebSocket
	// clients see real-time endpoint health transitions (degraded/recovered).
	eth.SetHealthCallback(func(endpointIdx int, healthy bool, total, healthyCount int) {
		bcast.Publish(sse.Event{
			Type: "rpc-health",
			Data: map[string]any{
				"endpoint":      endpointIdx,
				"healthy":       healthy,
				"total":         total,
				"healthy_count": healthyCount,
			},
		})
	})

	// serverTimeMs is updated atomically by the indexer watcher
	var serverTimeMs int64

	// Seed static NFT metadata from embedded JSON files at startup. This
	// eliminates the circular dependency where the metadata worker would
	// otherwise call tokenURI() on-chain, HTTP-fetch the same server's
	// static files, parse them, and store them — all of which is redundant
	// when the metadata is already available as embedded static files.
	//
	// The seeder reads files from frontend/static/nft/metadata/ and creates
	// DB entries for the NFT_ADDR collection. Once seeded, the metadata
	// worker finds no missing tokens for this collection and skips it.
	logStaticMetadataStatus()
	seedStaticMetadata(ctx, q, config.C.NFTAddr)

	// Seed tracked_collections at startup so the indexer watches Transfer events
	// for every NFT contract the operator explicitly configured (TRACKED_COLLECTIONS
	// comma-separated env var) AND every collection that already has rows in
	// nft_tokens (previously listed/auctioned collections whose tracking row may
	// have been lost). Without this, collections that have never been listed or
	// auctioned on MagicWebb are invisible to the indexer — their Transfer events
	// are never processed, nft_ownership stays empty, and WalletNFTs returns zero
	// results even when the wallet legitimately holds NFTs.
	seedTracked(ctx, q)

	// ── gRPC Keeper Election ──────────────────────────────────────────────
	// Replaces the Postgres advisory lock (db.WaitKeeperLock) with a
	// peer-to-peer gRPC heartbeat protocol. The election runs on the same
	// gRPC server as the event bridge so all inter-instance communication
	// shares one port and one set of peer connections.
	//
	// When no gRPC bridge is configured (standalone/single-instance mode),
	// the keeper election falls through to the old local-only behaviour:
	// the instance becomes leader immediately without any lock acquisition.
	var keeperElection *keeper.Election
	if grpcSrv := bcast.GRPCServer(); grpcSrv != nil && len(config.C.GRPCPeers) > 0 {
		// Determine this instance's address from GRPC_PEERS by finding our
		// own port. In multi-instance deployments, each instance knows its
		// own address so it can filter itself from the peer list.
		myAddr := fmt.Sprintf("localhost:%d", config.C.GRPCPort)
		keeperElection = keeper.New(grpcSrv, config.C.GRPCPeers, myAddr,
			keeper.WithEthClient(eth),
			keeper.WithMaxDegraded(3),
		)
		log.Info().Int("port", config.C.GRPCPort).Int("peers", len(config.C.GRPCPeers)).
			Msg("keeper: gRPC election enabled")
	} else {
		log.Info().Msg("keeper: gRPC election disabled (no peers), running local-only")
	}

	// Start indexer in background. Keepers gate on the gRPC election (when
	// multi-instance) or run immediately (single-instance). The KeeperGate
	// abstraction means runner.go doesn't need to change — it calls the gate
	// function, gets back a lockCtx, and cancels it when leadership is lost.
	runner := indexer.New(&config.C, q, bcast, eth, &serverTimeMs).
		WithKeeperGate(func(c context.Context) (context.Context, func(), error) {
			if keeperElection == nil {
				// Single-instance: no gate needed, keepers run immediately.
				return c, func() {}, nil
			}
			// Start (or restart) the election loop.
			if err := keeperElection.Run(c); err != nil {
				return nil, nil, err
			}
			return keeperElection.LockCtx(), func() {
				keeperElection.Release()
			}, nil
		})
	indexerDone := make(chan struct{})
	go func() {
		defer close(indexerDone)
		log.Info().Msg("indexer starting")
		runner.Run(ctx)
		log.Info().Msg("indexer stopped")
	}()

	// Fiber app. ProxyHeader=Fly-Client-IP plus EnableTrustedProxyCheck=false
	// makes `c.IP()` (and our api.rest clientIP helper) trust Fly.io's
	// reverse-proxy-stamped header — mathematically unspoofable from the
	// outside because Fly's edge strips any inbound copy. Combined with the
	// Forwarded / X-Forwarded-For fallback chain, this fixes the Priority
	// Stack `clientIpSpoof` 🟠 P1 (was: trivially spoofable rightmost-XFF
	// extraction when traffic bypassed the proxy).
	app := fiber.New(fiber.Config{
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          0, // SSE connections need no write timeout
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: false,
		EnableTrustedProxyCheck: false,
		ProxyHeader:           "Fly-Client-IP",
		// BodyLimit: 1 MB. Prevents oversized payload DoS attacks on JSON
		// endpoints (profile update, reports, auth verify). Fiber's default
		// limit is 4 MB — tightening to 1 MB means a degenerate JSON upload
		// cannot starve the connection pool or the Go GC. The limit applies
		// BEFORE any middleware or route handler touches the body — a 413
		// "Request Entity Too Large" is written directly by the framework.
		BodyLimit: 1 * 1024 * 1024, // 1 MB
	})

	// ── Monitoring middleware ───────────────────────────────────────────
	// Sentry recovery: captures panics and sends them to Sentry with full
	// request context. Must be registered BEFORE other middleware so it
	// can wrap the entire handler chain.
	if config.C.SentryDSN != "" {
		app.Use(sentryfiber.New(sentryfiber.Options{}))
	}
	// OTEL tracing: creates a span per HTTP request and propagates trace
	// context to downstream services. Registered after Sentry so trace IDs
	// are attached to error reports.
	if tp != nil {
		app.Use(otelTraceMiddleware())
	}

	// WH-3: Webhook event dispatcher — subscribes to the SSE Broadcaster and
	// fans out marketplace events to registered webhook URLs.
	whDispatcher := webhook.NewDispatcher(bcast, q, fmt.Sprintf("magicwebb:%d", config.C.GRPCPort))
	go whDispatcher.Start(ctx)

	// Mount all REST + SSE routes
	api.Mount(app, q, bcast, rl, &config.C, eth, &serverTimeMs, aks, al)

	// AUTH-3: API key management endpoints (admin-only, admin tier rate limit).
	api.MountAPIKeyRoutes(app, q, &config.C, rl, aks, al)

	// ── gRPC health service (standard grpc.health.v1.Health) ──────────────
	// Registered on the event bridge's gRPC server so Fly.io can probe this
	// instance with a sub-millisecond gRPC health check instead of a full HTTP
	// round-trip through /healthz. The health server probes DB + RPC + head lag.
	// Placed AFTER runner creation so runner.HeadLagBlocks is available.
	if grpcSrv := bcast.GRPCServer(); grpcSrv != nil {
		grpc_health_v1.RegisterHealthServer(grpcSrv, health.New(q, eth, runner.HeadLagBlocks))
		log.Info().Int("port", config.C.GRPCPort).Msg("grpc: health service registered")
	}

	// Admin-only endpoint: force re-scan of Transfer events for a specific
	// collection (mounted after runner creation so the handler can call
	// runner.ReindexCollection).
	api.MountReindexRoute(app, runner, &config.C)

	// Prometheus /metrics endpoint
	registerMetricsRoute(app, q, runner.HeadLagBlocks, eth, api.GlobalWSStats)

	// Register SLO (Prometheus gauge) and healthz (DB + RPC + lag threshold) routes.
	// Extracted into a function for unit testability — the getHeadLag callback
	// lets tests inject a controllable lag value instead of a real Runner.
	registerSLOHealthRoutes(app, q, eth, runner.HeadLagBlocks)

	// Auth endpoints with tighter rate limit (20 req/min per IP)
	app.Get("/auth/nonce", nonceHandler(ns, rl))
	app.Post("/auth/verify", verifyHandler(ns, rl, rs, al))
	app.Post("/auth/refresh", refreshHandler(rs, al))

	// Serve HTMX templates + static files
	mountUI(app, q, &serverTimeMs)

	// Graceful shutdown: SIGINT/SIGTERM stops accepting traffic, shuts down
	// the gRPC event bridge, then cancels ctx and WAITS for the indexer/keepers
	// to drain so no settle/refund broadcast is cut mid-flight and the advisory
	// lock releases cleanly.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		s := <-sig
		log.Info().Str("signal", s.String()).Msg("shutting down")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			log.Error().Err(err).Msg("http shutdown")
		}
		// Shut down the gRPC event bridge after HTTP so in-flight events
		// from the indexer can still drain before peer connections close.
		bcast.Shutdown()
	}()

	log.Info().Str("addr", config.C.HTTPAddr).Msg("server starting")
	if err := app.Listen(config.C.HTTPAddr); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}

	// ── Monitoring teardown ─────────────────────────────────────────────
	if config.C.SentryDSN != "" {
		sentry.Flush(2 * time.Second)
	}
	if tp != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("otel: shutdown failed")
		}
	}
	al.Close() // drain audit log queue before exit

	cancel()
	select {
	case <-indexerDone:
		log.Info().Msg("indexer drained")
	case <-time.After(15 * time.Second):
		log.Warn().Msg("indexer drain timed out")
	}
}

// ── Prometheus /metrics endpoint ────────────────────────────────────────────

// registerMetricsRoute mounts a Prometheus-compatible /metrics endpoint that
// exposes gauges and counters from across the system. Formatted as
// Prometheus text exposition format so standard scrapers (Prometheus,
// Grafana Agent, Datadog Agent with OpenMetrics check) can ingest it.
//
// Exported metrics:
//
//	magicwebb_sse_dropped_total      counter — events dropped due to fan-out saturation
//	magicwebb_sse_saturation_streak  gauge   — consecutive saturated Publish calls
//	magicwebb_head_lag_blocks        gauge   — indexer lag behind chain head
//	magicwebb_build_info             gauge   — 1, with sha label
//
// The endpoint is NOT rate-limited — Prometheus scrapes typically run every
// 15-60s and a rate limit would silently drop scrape intervals, creating gaps
// in dashboards.
func registerMetricsRoute(app *fiber.App, _ *db.Q, getHeadLag func() uint64, eth *rpcpool.Pool, wsStats api.WSStatsProvider) {
	app.Get("/metrics", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain; charset=utf-8")

		// SSE saturation metrics from the broadcaster.
		dropped := sse.DroppedTotal.Load()
		streak := sse.SaturationStreak.Load()

		// Indexer head lag.
		lag := uint64(0)
		if getHeadLag != nil {
			lag = getHeadLag()
		}

		// DB pool stats (approximate — pgxpool.Stat() not exposed through db.Q).
		// We surface build info and the key operational metrics that don't
		// require a DB round-trip.

		// CACHE-4: cache hit/miss/set/eviction counters.
		var cacheHits, cacheMisses, cacheSets, cacheEvictions int64
		if api.GlobalCaches.Trending != nil {
			ts := api.GlobalCaches.Trending.Stats()
			cacheHits += ts["cache_hits"]
			cacheMisses += ts["cache_misses"]
			cacheSets += ts["cache_sets"]
			cacheEvictions += ts["cache_evictions"]
		}
		if api.GlobalCaches.Activity != nil {
			as := api.GlobalCaches.Activity.Stats()
			cacheHits += as["cache_hits"]
			cacheMisses += as["cache_misses"]
			cacheSets += as["cache_sets"]
			cacheEvictions += as["cache_evictions"]
		}

		// WS metrics: active connections, lifetime connections, rate-limited messages,
		// and connection rejections (per-IP and global).
		var wsConns, wsTotalConns, wsMsgRateLimited, wsRejectedIP, wsRejectedGlobal int64
		if wsStats != nil {
			wsConns = int64(wsStats.ActiveConns())
			wsTotalConns = wsStats.TotalConns()
			wsMsgRateLimited = wsStats.MsgRateLimited()
			wsRejectedIP = wsStats.ConnsRejectedIP()
			wsRejectedGlobal = wsStats.ConnsRejectedGlobal()
		}

		return c.SendString(fmt.Sprintf(
			"# HELP magicwebb_sse_dropped_total Total SSE events dropped due to fan-out saturation.\n"+
				"# TYPE magicwebb_sse_dropped_total counter\n"+
				"magicwebb_sse_dropped_total %d\n"+
				"# HELP magicwebb_sse_saturation_streak Consecutive saturated Publish calls (0 when healthy).\n"+
				"# TYPE magicwebb_sse_saturation_streak gauge\n"+
				"magicwebb_sse_saturation_streak %d\n"+
				"# HELP magicwebb_sse_client_drops_total SSE-2: total events dropped due to slow WebSocket clients.\n"+
				"# TYPE magicwebb_sse_client_drops_total counter\n"+
			"magicwebb_sse_client_drops_total %d\n"+
			"# HELP magicwebb_rpc_healthy_count RPC-1: current number of healthy RPC endpoints.\n"+
			"# TYPE magicwebb_rpc_healthy_count gauge\n"+
			"magicwebb_rpc_healthy_count %d\n"+
			"# HELP magicwebb_head_lag_blocks Chain head minus last indexed block.\n"+
				"# TYPE magicwebb_head_lag_blocks gauge\n"+
				"magicwebb_head_lag_blocks %d\n"+
				"# HELP magicwebb_cache_hits_total Total cache hits across all caches.\n"+
				"# TYPE magicwebb_cache_hits_total counter\n"+
				"magicwebb_cache_hits_total %d\n"+
				"# HELP magicwebb_cache_misses_total Total cache misses across all caches.\n"+
				"# TYPE magicwebb_cache_misses_total counter\n"+
				"magicwebb_cache_misses_total %d\n"+
				"# HELP magicwebb_cache_sets_total Total cache sets across all caches.\n"+
				"# TYPE magicwebb_cache_sets_total counter\n"+
				"magicwebb_cache_sets_total %d\n"+
				"# HELP magicwebb_cache_evictions_total Total cache evictions (lazy TTL expiry).\n"+
				"# TYPE magicwebb_cache_evictions_total counter\n"+
				"magicwebb_cache_evictions_total %d\n"+
				"# HELP magicwebb_ws_active_connections Current active WebSocket connections.\n"+
				"# TYPE magicwebb_ws_active_connections gauge\n"+
				"magicwebb_ws_active_connections %d\n"+
				"# HELP magicwebb_ws_total_connections Lifetime WebSocket connections established.\n"+
				"# TYPE magicwebb_ws_total_connections counter\n"+
				"magicwebb_ws_total_connections %d\n"+
				"# HELP magicwebb_ws_msg_rate_limited Total client messages rejected by per-connection token bucket.\n"+
				"# TYPE magicwebb_ws_msg_rate_limited counter\n"+
				"magicwebb_ws_msg_rate_limited %d\n"+
				"# HELP magicwebb_ws_conns_rejected_ip Connection attempts rejected due to per-IP limit.\n"+
				"# TYPE magicwebb_ws_conns_rejected_ip counter\n"+
				"magicwebb_ws_conns_rejected_ip %d\n"+
				"# HELP magicwebb_ws_conns_rejected_global Connection attempts rejected due to global connection cap.\n"+
				"# TYPE magicwebb_ws_conns_rejected_global counter\n"+
				"magicwebb_ws_conns_rejected_global %d\n"+
				"# HELP magicwebb_build_info MagicWebb build metadata.\n"+
				"# TYPE magicwebb_build_info gauge\n"+
				"magicwebb_build_info{sha=\"%s\",env=\"%s\"} 1\n",
			dropped, streak, sse.DroppedClientsGauge(), eth.HealthyCount(), lag, cacheHits, cacheMisses, cacheSets, cacheEvictions,
			wsConns, wsTotalConns, wsMsgRateLimited, wsRejectedIP, wsRejectedGlobal,
			api.MWServerBuildSHA, config.C.Env,
		))
	})
}

// ── OTEL tracing middleware ──────────────────────────────────────────────

// otelTraceMiddleware returns a Fiber middleware that creates an OpenTelemetry
// span for every HTTP request. It extracts incoming trace context from W3C
// TraceContext headers (traceparent, tracestate) and creates a child span
// with http.method, http.url, http.status_code, and http.route attributes.
//
// This replaces the otelfiber contrib package, avoiding an external dependency
// whose module path changed between OTEL contrib versions. The logic is a
// minimal subset of the OTEL HTTP semantic conventions — sufficient for
// tracing request latency and error rates in any OTLP-compatible backend
// (Honeycomb, Grafana Tempo, Jaeger, Datadog).
func otelTraceMiddleware() fiber.Handler {
	tracer := otel.Tracer("magicwebb")
	return func(c *fiber.Ctx) error {
		// Extract incoming trace context from W3C headers.
		// fasthttp's GetReqHeaders() returns map[string][]string;
		// propagation.HeaderCarrier is http.Header which has the same
		// underlying type. Conversion is valid per Go spec.
		ctx := otel.GetTextMapPropagator().Extract(
			c.Context(),
			propagation.HeaderCarrier(c.GetReqHeaders()),
		)

		spanName := string(c.Request().Header.Method()) + " " + c.Path()
		route := c.Path()
		if r := c.Route(); r != nil {
			route = r.Path
		}
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", string(c.Request().Header.Method())),
				attribute.String("http.url", c.Request().URI().String()),
				attribute.String("http.route", route),
			),
		)
		defer span.End()

		// Store the span context in the request context so downstream
		// handlers (DB queries, RPC calls) can create child spans.
		c.SetUserContext(ctx)

		err := c.Next()

		// Record the response status on the span.
		statusCode := c.Response().StatusCode()
		span.SetAttributes(attribute.Int("http.status_code", statusCode))
		if statusCode >= 400 {
			span.SetStatus(codes.Error, "")
		}

		return err
	}
}

// ── Deployment Config Check ──────────────────────────────────────────────────

// verifyDeploymentConfig reads the latest deployment_config row and compares it
// to the running env config. On first deploy (no rows), it inserts the current
// config. On mismatch, it returns an error so main() can fatal-exit.
func verifyDeploymentConfig(ctx context.Context, pool db.PgxPool) error {
	var chainID int64
	var marketplaceAddr, auctionAddr, offerbookAddr, nftAddr, managerAddr string
	err := pool.QueryRow(ctx,
		`SELECT chain_id, marketplace_addr, auction_addr, offerbook_addr, nft_addr, marketplace_manager_addr
		   FROM deployment_config ORDER BY id DESC LIMIT 1`,
	).Scan(&chainID, &marketplaceAddr, &auctionAddr, &offerbookAddr, &nftAddr, &managerAddr)

	if err == pgx.ErrNoRows {
		// First deploy — insert the current config and proceed.
		_, err := pool.Exec(ctx,
			`INSERT INTO deployment_config(chain_id, marketplace_addr, auction_addr, offerbook_addr, nft_addr, marketplace_manager_addr)
			 VALUES($1,$2,$3,$4,$5,$6)`,
			config.C.ChainID, config.C.MarketplaceAddr, config.C.AuctionAddr,
			config.C.OfferBookAddr, config.C.NFTAddr, config.C.MarketplaceManagerAddr)
		if err != nil {
			return fmt.Errorf("insert initial deployment config: %w", err)
		}
		log.Info().Msg("deployment config: initial row created")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read deployment config: %w", err)
	}

	// Compare each field. Collect all mismatches before returning.
	var diffs []string
	if chainID != int64(config.C.ChainID) {
		diffs = append(diffs, fmt.Sprintf("chain_id: stored=%d env=%d", chainID, config.C.ChainID))
	}
	if marketplaceAddr != config.C.MarketplaceAddr {
		diffs = append(diffs, fmt.Sprintf("marketplace_addr: stored=%s env=%s", marketplaceAddr, config.C.MarketplaceAddr))
	}
	if auctionAddr != config.C.AuctionAddr {
		diffs = append(diffs, fmt.Sprintf("auction_addr: stored=%s env=%s", auctionAddr, config.C.AuctionAddr))
	}
	if offerbookAddr != config.C.OfferBookAddr {
		diffs = append(diffs, fmt.Sprintf("offerbook_addr: stored=%s env=%s", offerbookAddr, config.C.OfferBookAddr))
	}
	if nftAddr != config.C.NFTAddr {
		diffs = append(diffs, fmt.Sprintf("nft_addr: stored=%s env=%s", nftAddr, config.C.NFTAddr))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("deployment mismatch:\n  %s", strings.Join(diffs, "\n  "))
	}

	log.Info().Msg("deployment config: matches env — proceeding")
	return nil
}

// ── SIWE auth ────────────────────────────────────────────────────────────────

type nonceResp struct {
	Nonce string `json:"nonce"`
}

type verifyReq struct {
	Address   string `json:"address"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

type tokenResp struct {
	Token   string `json:"token"`
	Address string `json:"address"`
}

func nonceHandler(ns nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := api.ClientIP(c)
		if !rl.Allow("auth:"+ip, 20, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		address := strings.ToLower(strings.TrimSpace(c.Query("address")))
		if address == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address required"})
		}
		if !isValidEthAddr(address) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid address format"})
		}
		// Cryptographically random 16-byte nonce. crypto/rand per RFC 4086.
		var rb [16]byte
		if _, err := rand.Read(rb[:]); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "rng failed"})
		}
		n := hex.EncodeToString(rb[:])
		if !ns.SetIfFree(address, n, config.C.NonceTTL) {
			// Live nonce exists — caller must consume it first. Rate-limit
			// prevents tight retry loops from a legitimate user.
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "nonce already issued, consume it or wait for expiry"})
		}
		return c.JSON(nonceResp{Nonce: n})
	}
}

func verifyHandler(ns nonce.Store, rl *ratelimit.Limiter, rs auth.RefreshStore, al auth.AuditLogger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := api.ClientIP(c)
		ua := c.Get("User-Agent")
		if !rl.Allow("auth:"+ip, 20, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		var req verifyReq
		if err := json.Unmarshal(c.Body(), &req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bad request"})
		}
		addr := strings.ToLower(req.Address)
		if !isValidEthAddr(addr) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid address format"})
		}

		// L-15 fix: perform ALL validation BEFORE consuming the nonce.
		// The previous ordering called ns.GetDel first, which consumed
		// the nonce before domain/chain/signature validation. If any
		// of those validations failed, the nonce was already consumed
		// and the legitimate user had to request a new one — adding an
		// unnecessary round trip. Worse, if an attacker could trigger
		// validation failures deliberately (e.g. by submitting a message
		// with an invalid domain), they could DOS a real user by
		// repeatedly consuming their nonce without ever completing auth.
		// The fix: validate domain, chain ID, and ECDSA signature BEFORE
		// consuming the nonce. On any validation failure, the nonce is
		// never touched and the legitimate user can retry immediately.

		// Bind the signed message to our domain so a signature obtained for another
		// site cannot be replayed here (SIWE domain binding).
		// R-07 fix: use structured EIP-4361 parsing instead of substring match.
		// The previous `strings.Contains(req.Message, d)` was vulnerable to
		// cross-application replay: an attacker could trick a user into signing
		// a SIWE message for attacker.com with the target domain embedded in
		// the Statement or URI fields, and the substring check would pass.
		// The fix extracts the domain from the EIP-4361 `domain` line (the
		// first line of the message, before " wants you to sign in...") and
		// requires an exact match. Falls back to substring match if the message
		// doesn't follow EIP-4361 format (legacy compatibility).
		if d := config.C.SIWEDomain; d != "" {
			if !siweDomainMatches(req.Message, d) {
				auth.AuditLoginFailed(al, addr, ip, ua, "domain_mismatch", map[string]string{"domain": d})
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "domain mismatch"})
			}
		}
		// v29 audit F-01: bind the signature to the running chain. Without
		// this, a signature replays as valid on another chain because
		// the (message, signature, address) tuple is identical except for the
		// chainId line. The wallet.js SIWE template now includes
		// `Chain ID: N`; we require N == config.C.ChainID. Reject before
		// EIP-191 so a forged-claim signature can't even burn signature-verify
		// cycles.
		// R-09 fix: use structured EIP-4361 line parsing instead of substring
		// match. The previous `strings.Contains(req.Message, wantSubstr)` was
		// vulnerable to cross-chain replay: an attacker could trick a user
		// into signing a SIWE message for chain 1 with "Chain ID: 114"
		// embedded in the URI or Statement field, and the substring check
		// would pass. The fix extracts the Chain ID from the EIP-4361
		// `Chain ID: N` line and requires an exact integer match. Falls back
		// to substring match if the message doesn't follow EIP-4361 format
		// (legacy compatibility).
		if want := config.C.ChainID; want != 0 {
			if !siweChainIDMatches(req.Message, want) {
				auth.AuditLoginFailed(al, addr, ip, ua, "chain_id_mismatch", map[string]string{"expected": fmt.Sprintf("%d", want)})
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "chain id mismatch"})
			}
		}
		ok, err := verifyEIP191(req.Message, req.Signature, req.Address)
		if err != nil || !ok {
			auth.AuditLoginFailed(al, addr, ip, ua, "invalid_signature", nil)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "signature verification failed"})
		}

		// All validations passed — NOW consume the nonce (single-use).
		n, found := ns.GetDel(addr)
		if !found {
			auth.AuditLoginFailed(al, addr, ip, ua, "nonce_consumed", nil)
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "nonce already consumed or expired, request a new one"})
		}
		if !strings.Contains(req.Message, n) {
			auth.AuditLoginFailed(al, addr, ip, ua, "nonce_mismatch", nil)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "nonce mismatch"})
		}

		// AUTH-1: issue short-lived access token (15min) + long-lived refresh
		// token (7d) with family-based rotation. The refresh token's jti claim
		// maps to the token_id in refresh_token_families, enabling atomic
		// rotation and replay detection on every /auth/refresh call.
		familyID, tokenID, err := rs.IssueRefreshFamily(c.Context(), addr, auth.RefreshTokenTTL)
		if err != nil {
			log.Error().Err(err).Str("addr", addr).Msg("refresh family creation failed")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "session creation failed"})
		}

		accessToken, err := auth.IssueAccessToken(addr, config.C.JWTSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}

		refreshToken, err := auth.IssueRefreshTokenWithFamily(addr, config.C.JWTSecret, familyID, tokenID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}

		// Set both cookies: access (15min) + refresh (7d). The refresh cookie
		// is Path=/auth only so it's never sent on data endpoints, reducing
		// exposure. The access cookie is Path=/ for API authorization.
		setSessionCookie(c, addr, accessToken)
		setRefreshCookie(c, addr, refreshToken)
		auth.AuditLoginSuccess(al, addr, ip, ua)
		return c.JSON(tokenResp{Token: accessToken, Address: addr})
	}
}

// setSessionCookie writes the access-token auth cookie with hardening defaults:
// HttpOnly (no JS access), Secure (always-on in production; in dev, only if
// the request itself was HTTPS — fiber's c.Protocol() is forwarded-scheme-
// aware), SameSite=Lax (no cross-site mutating requests), Path=/, Max-Age=15min
// (matches AccessTokenTTL). The cookie name encodes the wallet so multiple
// wallets coexisting in one browser resolve cleanly.
//
// The previous implementation keyed Secure on `c.Protocol() != "http"`,
// which is wrong behind a misconfigured TLS-terminating proxy that forwards
// the wrong scheme. Force Secure=true whenever ENV=production so a downgrade
// to plaintext auth is impossible even if the proxy is buggy.
func setSessionCookie(c *fiber.Ctx, address, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameAccess(address),
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		Secure:   config.C.Env == "production" || c.Protocol() == "https",
		SameSite: "Lax",
		MaxAge:   int((15 * time.Minute).Seconds()),
	})
}

// setRefreshCookie writes the refresh-token cookie with stricter scope:
// Path=/auth so the browser never sends the long-lived refresh token on
// data endpoints, minimising exposure to XSS and CSRF. Other defaults
// (HttpOnly, Secure, SameSite=Lax) mirror setSessionCookie.
// MaxAge=7d matches RefreshTokenTTL.
func setRefreshCookie(c *fiber.Ctx, address, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameRefresh(address),
		Value:    token,
		Path:     "/auth",
		HTTPOnly: true,
		Secure:   config.C.Env == "production" || c.Protocol() == "https",
		SameSite: "Lax",
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
}

// ── Refresh token handler ────────────────────────────────────────────────

// refreshHandler implements POST /auth/refresh with family-based rotation.
// It reads the refresh cookie (mw_r_<addr>), verifies the JWT, extracts
// family_id and jti from claims, atomically rotates the token, and issues
// a new access+refresh pair. On replay (reused old token), the entire
// family is revoked — forcing re-authentication.
func refreshHandler(rs auth.RefreshStore, al auth.AuditLogger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := api.ClientIP(c)
		ua := c.Get("User-Agent")

		// Scan the Cookie header for mw_r_* (refresh cookie).
		// c.Cookies() returns the raw header string; parse manually.
		var refreshToken string
		for _, cookie := range strings.Split(c.Get("Cookie"), ";") {
			cookie = strings.TrimSpace(cookie)
			if strings.HasPrefix(strings.ToLower(cookie), "mw_r_") {
				if idx := strings.IndexByte(cookie, '='); idx > 0 {
					refreshToken = cookie[idx+1:]
					break
				}
			}
		}

		// Also check Authorization header for Bearer refresh tokens
		if refreshToken == "" {
			if hdr := c.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
				refreshToken = strings.TrimPrefix(hdr, "Bearer ")
			}
		}

		if refreshToken == "" {
			// Cannot determine wallet address without parsing the token —
			// log with empty addr so IP+UA still help identify patterns.
			auth.AuditRefreshFailed(al, "", ip, ua, "no_token", nil)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "no refresh token"})
		}

		// Verify the refresh JWT (audience: magicwebb:refresh, token_type: refresh).
		addr, _, err := auth.Verify(refreshToken, config.C.JWTSecret, auth.RefreshAudience)
		if err != nil {
			auth.AuditRefreshFailed(al, "", ip, ua, "invalid_token", nil)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid refresh token"})
		}

		// Extract family_id and jti from claims for rotation tracking.
		familyID, tokenID := auth.ParseRefreshClaims(refreshToken, config.C.JWTSecret)
		if familyID == "" || tokenID == "" {
			auth.AuditRefreshFailed(al, addr, ip, ua, "malformed_claims", nil)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "malformed refresh token"})
		}

		// Atomically rotate: revoke old token_id, issue new one in same family.
		newTokenID, err := rs.RotateRefreshToken(c.Context(), familyID, tokenID, auth.RefreshTokenTTL)
		if err != nil {
			log.Warn().Err(err).Str("addr", addr).Str("family", familyID).Msg("refresh rotation failed")
			auth.AuditRefreshFailed(al, addr, ip, ua, "rotation_rejected", map[string]string{"error": err.Error()})
			// Clear both cookies so the client knows to re-authenticate.
			clearAuthCookies(c, addr)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "refresh token rejected — re-authenticate"})
		}

		// Issue new access + refresh tokens.
		accessToken, err := auth.IssueAccessToken(addr, config.C.JWTSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}

		newRefreshToken, err := auth.IssueRefreshTokenWithFamily(addr, config.C.JWTSecret, familyID, newTokenID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}

		setSessionCookie(c, addr, accessToken)
		setRefreshCookie(c, addr, newRefreshToken)
		auth.AuditRefreshSuccess(al, addr, ip, ua)
		return c.JSON(tokenResp{Token: accessToken, Address: addr})
	}
}

// clearAuthCookies removes access and refresh cookies to force
// re-authentication (used on rotation failure / replay detection).
// Clears both legacy (mw_s_) and current (mw_a_) access cookie names.
func clearAuthCookies(c *fiber.Ctx, address string) {
	// Current access cookie (mw_a_<prefix>).
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameAccess(address),
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		MaxAge:   -1,
	})
	// Legacy access cookie (mw_s_<prefix>) — clear for sessions created
	// before the cookie name migration (AUTH-1).
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieName(address),
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		MaxAge:   -1,
	})
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameRefresh(address),
		Value:    "",
		Path:     "/auth",
		HTTPOnly: true,
		MaxAge:   -1,
	})
}

// isValidEthAddr validates a lowercase Ethereum address: 0x + 40 lowercase hex chars.
func isValidEthAddr(s string) bool {
	if len(s) != 42 || s[:2] != "0x" {
		return false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// seedTracked ensures every collection the indexer needs to watch has a row in
// tracked_collections. It merges three sources at startup:
//   1. Explicit TRACKED_COLLECTIONS env var (operator-configured NFT contracts)
//   2. Every unique collection that already appears in nft_tokens (covers
//      collections that were ever listed, auctioned, or had a Transfer event
//      indexed at some point — even if their tracked_collections row was later
//      lost or never explicitly seeded)
// A missing tracked_collections row means the indexer's processTransfers() skips
// that contract entirely, so nft_ownership stays empty and WalletNFTs returns
// zero results even when the wallet holds tokens. This seed runs once per
// process lifetime at startup; EnsureCollection is idempotent (ON CONFLICT DO
// NOTHING) so repeated seeds on restart are cheap.
func seedTracked(ctx context.Context, q *db.Q) {
	addrs := config.C.TrackedCollections // env TRACKED_COLLECTIONS

	// 1. Existing nft_tokens collections (auto-discovered).
	if existing, err := q.ListDistinctCollectionsFromTokens(ctx); err == nil {
		addrs = append(addrs, existing...)
	} else {
		log.Warn().Err(err).Msg("startup: ListDistinctCollectionsFromTokens failed, skipping auto-seed")
	}

	// 2. Dedup.
	seen := make(map[string]bool, len(addrs))
	uniq := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		uniq = append(uniq, a)
	}

	if len(uniq) == 0 {
		return
	}

	seeded := q.SeedTrackedCollections(ctx, uniq)
	log.Info().Int("attempted", len(uniq)).Int("seeded", seeded).
		Msg("startup: seeded tracked_collections")
}

// siweDomainMatches extracts the domain from an EIP-4361 SIWE message
// and checks for an exact match. The domain is the first line of the
// message, which must end with " wants you to sign in with your Ethereum
// account:". Messages that don't follow EIP-4361 format are rejected
// outright — no legacy substring fallback, to prevent cross-application
// replay attacks where the target domain is embedded in a Statement or
// URI field (R-07).
func siweDomainMatches(msg, expected string) bool {
	// EIP-4361: first line is "<domain> wants you to sign in with your Ethereum account:"
	idx := strings.Index(msg, " wants you to sign in")
	if idx <= 0 {
		return false
	}
	domain := msg[:idx]
	return domain == expected
}

// siweChainIDMatches extracts the Chain ID from an EIP-4361 SIWE message
// and checks for an exact integer match. It parses the line starting with
// "Chain ID: ". Messages that don't contain a parseable Chain ID line are
// rejected outright — no legacy substring fallback, to prevent cross-chain
// replay attacks where the target chain ID text is embedded in a URI or
// Statement field (R-12).
func siweChainIDMatches(msg string, expected uint64) bool {
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Chain ID: ") {
			idStr := strings.TrimSpace(strings.TrimPrefix(line, "Chain ID: "))
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				return id == expected
			}
			// Chain ID line exists but can't be parsed — reject.
			return false
		}
	}
	// No Chain ID line found — reject.
	return false
}

func verifyEIP191(message, sigHex, address string) (bool, error) {
	msg := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256Hash([]byte(msg))
	sigBytes, err := hexutil.Decode(sigHex)
	if err != nil || len(sigBytes) != 65 {
		return false, fmt.Errorf("invalid signature")
	}
	sig := make([]byte, 65)
	copy(sig, sigBytes)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pubKey, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return false, err
	}
	recovered := crypto.PubkeyToAddress(*pubKey)
	return strings.EqualFold(recovered.Hex(), address), nil
}

// ── SLO + Healthz routes (testable via registerSLOHealthRoutes) ────────────────

// registerSLOHealthRoutes mounts two endpoints:
//   /api/v1/indexer/slo — Prometheus-format head_lag_blocks gauge
//   /healthz            — DB ping + RPC block number + head-lag threshold (503 when >15)
//
// The getHeadLag callback lets tests inject a controllable lag value without needing
// a real indexer.Runner. Production passes runner.HeadLagBlocks.
func registerSLOHealthRoutes(app *fiber.App, q *db.Q, eth indexer.EthClient, getHeadLag func() uint64) {
	// Expose head lag as a Prometheus-compatible text gauge.
	app.Get("/api/v1/indexer/slo", func(c *fiber.Ctx) error {
		lag := getHeadLag()
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(fmt.Sprintf(
			"# HELP head_lag_blocks Chain head minus last indexed block\n"+
				"# TYPE head_lag_blocks gauge\n"+
				"head_lag_blocks %d\n", lag))
	})

	// Override /healthz to also report indexer head lag SLO (Fiber LIFO route
	// resolution means this runs after api.Mount's /healthz, effectively wrapping it).
	app.Get("/healthz", func(c *fiber.Ctx) error {
		pingCtx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		if err := q.Ping(pingCtx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("db unhealthy")
		}
		rpcCtx, cancelRPC := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancelRPC()
		if _, err := eth.BlockNumber(rpcCtx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("rpc unhealthy")
		}
		// Indexer lag SLO: warn when more than 15 blocks behind the head.
		// On Flare/Coston2 (~2s block time), 15 blocks ≈ 30 seconds of lag.
		if lag := getHeadLag(); lag > 15 {
			return c.Status(fiber.StatusServiceUnavailable).
				SendString(fmt.Sprintf("indexer lag: %d blocks behind head", lag))
		}
		c.Set("X-MW-Build-SHA", api.MWServerBuildSHA)
		return c.SendStatus(fiber.StatusOK)
	})
}

// ── UI (HTMX pages + static) ──────────────────────────────────────────────────

func mountUI(app *fiber.App, q *db.Q, serverTimeMs *int64) {
	// Initialize frontend templates with media URL function.
	// Must happen before any template rendering.
	frontend.Init(media.ProxyURL)

	// Static files from embedded FS
	mountStatic(app)

	// Astro-built Svelte/React pages from app/dist/ at /app/* URL prefix.
	// Set ASTRO_DIST_DIR env var to the build output path (defaults to
	// "../app/dist" for dev; use "/app/dist" in the Docker image).
	mountAstro(app)

	// HTMX pages — server-rendered HTML
	app.Get("/", uiHome(q))
	app.Get("/listings", uiListings(q))
	app.Get("/auctions", uiAuctions(q))
	app.Get("/auction/:id", uiAuctionDetail(q))
	app.Get("/offers", uiOffers(q))
	app.Get("/profile/:addr", uiProfile(q))
	// /profile (no addr) — rescue route. Resolves to /profile/<own addr>
	// when a valid SIWE session cookie is present, else /listings. See
	// uiProfileRedirect for the security rationale.
	app.Get("/profile", uiProfileRedirect)
	app.Get("/collection/:addr", uiCollection(q))
	app.Get("/token/:addr/:id", uiToken(q))
	app.Get("/search", uiSearch(q))
	app.Get("/metrics", uiMetrics(q))
	app.Get("/metrics/gas", uiGasMetrics(q))
	app.Get("/admin/stalled", uiAdminStalled(q))
	app.Get("/docs", uiDocsIndex())
	app.Get("/docs/:slug", uiDoc())

	// HTMX partials (return HTML fragments for hx-get). New per-page live
	// partials render the same data shape as their page handler so htmx
	// can swap them into `[data-live]` outerHTML every 1s OR on the SSE
	// event types each page subscribes to.
	app.Get("/partials/listings", partialListings(q))
	app.Get("/partials/auctions", partialAuctions(q))
	app.Get("/partials/activity", partialActivity(q))
	app.Get("/partials/token/:addr/:id", partialToken(q))
	app.Get("/partials/auction/:id", partialAuctionDetail(q))
	app.Get("/partials/offers", partialOffers(q))
	app.Get("/partials/profile/:addr", partialProfile(q))

	// v35: /api/v1/server-time moved to the rate-limited api group in rest.go.
	// Previously registered on the bare app, bypassing rateLimitMiddleware entirely.
}
