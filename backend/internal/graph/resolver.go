package graph

import "github.com/jackc/pgx/v5/pgxpool"

// Resolver is the root gqlgen resolver. Wired manually after `make codegen-graphql`.
// gqlgen generates `*.resolvers.go` files alongside this; this struct holds shared deps.
type Resolver struct {
	Pool *pgxpool.Pool
}
