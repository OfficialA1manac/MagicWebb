package main

import (
	"context"
	"log"
	"net"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	_ = godotenv.Load("../.env")

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	listen := envOr("GRPC_LISTEN", ":9090")
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	s := grpc.NewServer()
	reflection.Register(s)
	// TODO: register Matcher + Pricing servers after `make codegen-grpc`.
	// matcherpb.RegisterMatcherServer(s, &MatcherServer{Pool: pool})
	// pricingpb.RegisterPricingServer(s, &PricingServer{Pool: pool})

	log.Printf("matcher gRPC on %s (reflection enabled)", listen)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
