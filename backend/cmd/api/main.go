package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

func main() {
	// Structured JSON logs in prod; pretty-print in dev.
	if os.Getenv("ENV") != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	grpcAddr := envOrDefault("GRPC_ADDR", ":9090")
	httpAddr := envOrDefault("HTTP_ADDR", ":8080")

	// ── gRPC server ───────────────────────────────────────────────────────
	grpcLn, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", grpcAddr).Msg("grpc listen")
	}
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(recoveryUnary, otelUnary, logUnary),
		grpc.ChainStreamInterceptor(recoveryStream, otelStream),
	)
	// TODO D4: register service implementations
	// marketplacev1.RegisterMarketplaceServiceServer(grpcSrv, &marketplace.Server{})
	// auctionv1.RegisterAuctionServiceServer(grpcSrv, &auction.Server{})
	// offersv1.RegisterOffersServiceServer(grpcSrv, &offers.Server{})
	// indexerv1.RegisterIndexerServiceServer(grpcSrv, &indexer.StatusServer{})

	// ── GraphQL / HTTP server ─────────────────────────────────────────────
	mux := http.NewServeMux()
	// TODO D4: mount gqlgen handler, WebSocket gateway, health endpoints
	// mux.Handle("/graphql", graphqlHandler)
	// mux.HandleFunc("/healthz", healthHandler)
	// mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// ── Start both ────────────────────────────────────────────────────────
	go func() {
		log.Info().Str("addr", grpcAddr).Msg("gRPC listening")
		if err := grpcSrv.Serve(grpcLn); err != nil {
			log.Error().Err(err).Msg("gRPC serve")
		}
	}()
	go func() {
		log.Info().Str("addr", httpAddr).Msg("HTTP listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("HTTP serve")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	grpcSrv.GracefulStop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Error().Err(err).Msg("HTTP shutdown")
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Interceptor stubs — implemented in D4.
func recoveryUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(ctx, req)
}
func otelUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(ctx, req)
}
func logUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(ctx, req)
}
func recoveryStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, ss)
}
func otelStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, ss)
}
