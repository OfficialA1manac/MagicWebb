//go:build tools

// Package tools pins build-time tool dependencies (gqlgen codegen) so
// `go mod tidy` keeps their modules in go.sum. Never compiled into the binary.
package tools

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/graphql/introspection"
)
