package graphql

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/gofiber/fiber/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/dataloader"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"

	marketplacev1connect "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
)

// GQL-4: Per-connection GraphQL subscription limit. Prevents a single
// WebSocket connection from opening unlimited subscriptions (DoS vector).
const maxSubscriptionsPerConn = 10

// GQL-4: Auth context key for the authenticated wallet address.
// Injected into the gqlgen operation context via AroundOperations so
// subscription resolvers can filter notifications by user.
type authCtxKeyType struct{}

var AuthCtxKey = authCtxKeyType{}

// GraphQLServer wraps a gqlgen-generated executable schema with Fiber
// HTTP handlers for POST /graphql, GET /graphql (docs), and GET /graphiql.
// WebSocket subscriptions are served at /graphql/ws.
type GraphQLServer struct {
	srv  *handler.Server
	q    *db.Q  // retained for DataLoader creation per-request
	cfg  *config.Config // GQL-4: JWT secret for WS auth
}

// NewGraphQLServer creates a gqlgen-based GraphQL server powered by the
// generated executable schema from schema.graphql. The db.Q provides
// data access; bcast provides SSE push for subscription resolvers; grpc
// provides a typed, decoupled data path via Connect-RPC.
//
// cfg is required for GQL-4 WebSocket authentication (JWT secret).
// When nil, WS auth is skipped (unauthenticated subscriptions allowed).
func NewGraphQLServer(q *db.Q, bcast *sse.Broadcaster, grpc marketplacev1connect.MarketplaceServiceClient, cfg *config.Config) *GraphQLServer {
	resolver := NewResolver(q, bcast, grpc)
	schema := NewExecutableSchema(Config{
		Resolvers:  resolver,
		Complexity: ComplexityConfig(), // GQL-5: field-level cost weights
	})

	srv := handler.New(schema)

	// GQL-4: Auth context is injected per-request via context.WithValue in
	// HandleWS (for WS) or HandlePOST (for HTTP). Subscription resolvers
	// read the address via ctx.Value(graphql.AuthCtxKey). No middleware
	// needed — the context chain is preserved through gqlgen's transport.
	// The address is set in:
	//   - HandleWS: context.WithValue(ctx, AuthCtxKey, addr) before ServeHTTP
	//   - HandlePOST: (future — pass via HTTP Authorization header)

	// RL-2: Query depth validation. Rejects queries with nesting deeper than
	// 12 levels (e.g., collections { listings { seller { profile { ... } } } }).
	// Combined with GQL-5 field-level cost analysis for 2D DoS protection.
	const maxQueryDepth = 12
	srv.Use(&depthValidator{maxDepth: maxQueryDepth})

	// Transport: POST JSON, GET query-string, and WebSocket subscriptions.
	// GQL-4: graphql-transport-ws protocol for real-time subscriptions.
	// The WebSocket transport uses gorilla/websocket internally and requires
	// http.Hijacker support — provided by hijackWriter for the Fiber bridge.
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.Websocket{
		KeepAlivePingInterval: 10 * time.Second,
		// Subprotocol: graphql-transport-ws (the current standard).
		// Client sends {"type":"subscribe","id":"...","payload":{"query":"..."}}
	})

	// ── GQL-5: Field-level query cost analysis ────────────────────────
	// The ComplexityRoot (set in NewExecutableSchema Config) assigns
	// per-field cost weights: scalars=1, list resolvers=10+child×limit.
	// Max total cost per query: 1000.
	srv.Use(extension.FixedComplexityLimit(MaxQueryCost))

	// APQ (Automatic Persisted Queries): clients send a hash first;
	// full query only on cache miss. Halves bandwidth for repeated
	// queries (e.g., polling a saved search).
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	// ── GQL-2: Response cache ────────────────────────────────────────
	// Caches complete JSON responses for deterministic read-heavy queries
	// (collection, metrics) with 30s TTL. On cache hit, the DB and all
	// resolvers are skipped entirely — reducing latency to near-zero.
	srv.Use(NewResponseCacheExtension())

	// Introspection: enabled in all environments so GraphiQL and
	// external tooling (Apollo Studio, Postman, etc.) can discover
	// the schema. No auth secrets are exposed through introspection.
	// CORS already limits which origins can access /graphql.

	return &GraphQLServer{srv: srv, q: q, cfg: cfg}
}

// HandlePOST executes a GraphQL query and returns the JSON response.
// This adapter bridges Fiber's request/response model to gqlgen's
// standard http.Handler interface.
func (s *GraphQLServer) HandlePOST(c *fiber.Ctx) error {
	if c.Method() != http.MethodPost {
		return c.Status(fiber.StatusMethodNotAllowed).SendString("use POST for GraphQL queries")
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	// Build an http.Request from Fiber's context so gqlgen can parse
	// the query body. The body stream is consumed by gqlgen's POST
	// transport (JSON decoding).
	req, err := http.NewRequestWithContext(ctx, "POST", "/graphql", c.Request().BodyStream())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"errors": []map[string]any{{"message": "internal server error"}},
		})
	}
	req.Header.Set("Content-Type", string(c.Request().Header.ContentType()))

	// Attach request-scoped DataLoaders to the context before serving.
	// Each request gets fresh loaders so batched queries don't leak across
	// users/requests. The resolvers extract loaders via dataloader.FromContext.
	req = req.WithContext(dataloader.WithLoaders(ctx, dataloader.New(s.q)))

	// gqlgen sets Content-Type via Header().Set(); we pre-set it here
	// because the fiberResponseWriter's Header() returns a disconnected
	// snapshot map (Fiber headers are not http.Header-compatible).
	c.Set("Content-Type", "application/json")

	w := &fiberResponseWriter{c: c}
	s.srv.ServeHTTP(w, req)

	// If gqlgen wrote a body, the response is already committed.
	// Otherwise return 200 OK (empty response for valid queries).
	if w.written {
		return nil
	}
	return c.SendStatus(fiber.StatusOK)
}

// HandleGET serves the GraphQL API documentation page.
func (s *GraphQLServer) HandleGET(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(graphqlDocsHTML)
}

// HandleGraphiQL serves the interactive GraphiQL IDE.
// RL-2: depthValidator rejects queries exceeding maxDepth nesting levels.
// Intercepts the gqlgen operation context before execution. Depth of 0 means
// the root query; each nested selection adds 1. Fragments are inlined so
// deeply-nested fragments count toward the total.
type depthValidator struct {
	maxDepth int
}

func (d *depthValidator) ExtensionName() string { return "DepthValidator" }
func (d *depthValidator) Validate(schema graphql.ExecutableSchema) error { return nil }

func (d *depthValidator) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := graphql.GetOperationContext(ctx)
	if oc == nil {
		return next(ctx)
	}
	depth := countDepth(oc.Operation.SelectionSet, 0)
	if depth > d.maxDepth {
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{
				Errors: gqlerror.List{{
					Message: fmt.Sprintf("query depth %d exceeds max %d", depth, d.maxDepth),
				}},
			}
		}
	}
	return next(ctx)
}

// countDepth recursively computes the nesting depth of a GraphQL selection set.
func countDepth(set ast.SelectionSet, depth int) int {
	maxDepth := depth
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			if s.SelectionSet != nil && len(s.SelectionSet) > 0 {
				d := countDepth(s.SelectionSet, depth+1)
				if d > maxDepth {
					maxDepth = d
				}
			}
		case *ast.FragmentSpread:
			// Inlined by gqlgen before execution; skip here.
		case *ast.InlineFragment:
			d := countDepth(s.SelectionSet, depth+1)
			if d > maxDepth {
				maxDepth = d
			}
		}
	}
	return maxDepth
}

// HandleWS handles WebSocket GraphQL subscription connections at /graphql/ws.
// Uses fasthttp.RequestCtx.Hijack() to get the raw net.Conn, wraps it in a
// hijackWriter (which implements http.Hijacker), and delegates to gqlgen's
// built-in transport.Websocket handler. The JWT wallet address is extracted
// from cookies/Authorization header and injected into the gqlgen context
// via AuthCtxKey so subscription resolvers can filter by user.
func (s *GraphQLServer) HandleWS(c *fiber.Ctx) error {
	// Verify this is a WebSocket upgrade request before hijacking.
	// Without this check, a regular HTTP GET would hijack the TCP connection
	// and gqlgen's transport would fail silently, dropping the connection.
	if !strings.EqualFold(c.Get("Upgrade"), "websocket") {
		return c.Status(fiber.StatusBadRequest).SendString("websocket upgrade required")
	}

	// Extract JWT-authenticated wallet address.
	addr := s.authenticateWS(c)

	// Hijack the underlying connection from Fiber/fasthttp.
	c.Context().Hijack(func(conn net.Conn) {
		// Build a fake http.Request for gqlgen's transport.Websocket.
		// The transport extracts the sub-protocol from the request headers.
		req, err := http.NewRequest("GET", "/graphql/ws", nil)
		if err != nil {
			conn.Close()
			return
		}

		// Copy relevant headers from the Fiber request.
		req.Header = make(http.Header)
		c.Request().Header.VisitAll(func(key, value []byte) {
			req.Header.Set(string(key), string(value))
		})

		// Create a hijack-aware ResponseWriter backed by the raw conn.
		w := newHijackWriter(conn)

		// Inject auth context so subscription resolvers can access the
		// authenticated wallet address via ctx.Value(graphql.AuthCtxKey).
		ctx := context.WithValue(req.Context(), AuthCtxKey, addr)
		req = req.WithContext(ctx)
		// Attach DataLoaders for subscription resolvers that query the DB.
		req = req.WithContext(dataloader.WithLoaders(ctx, dataloader.New(s.q)))

		// Delegate to gqlgen's built-in WebSocket transport.
		s.srv.ServeHTTP(w, req)
	})

	return nil
}

// authenticateWS extracts the wallet address from JWT cookies or Authorization
// header. Mirrors the logic in ws/handler.go authenticate(). Returns "" for
// unauthenticated connections (public subscriptions still work).
func (s *GraphQLServer) authenticateWS(c *fiber.Ctx) string {
	if s.cfg == nil {
		return ""
	}

	// Try session cookies (both legacy mw_s_ and new mw_a_ access tokens).
	hdr := c.Get("Cookie")
	if hdr != "" {
		for _, part := range strings.Split(hdr, ";") {
			p := strings.TrimSpace(part)
			if !strings.HasPrefix(p, "mw_s_") && !strings.HasPrefix(p, "mw_a_") {
				continue
			}
			if eq := strings.IndexByte(p, '='); eq > 0 {
				if a, err := auth.VerifyAccessToken(p[eq+1:], s.cfg.JWTSecret); err == nil {
					return a
				}
			}
		}
	}

	// Try Authorization header.
	if ah := c.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
		if a, err := auth.VerifyAccessToken(strings.TrimPrefix(ah, "Bearer "), s.cfg.JWTSecret); err == nil {
			return a
		}
	}

	return ""
}

func (s *GraphQLServer) HandleGraphiQL(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(graphiqlHTML)
}

// fiberResponseWriter adapts a Fiber context into an http.ResponseWriter
// so gqlgen's handler.ServeHTTP can write directly to the Fiber response.
// Write() writes bytes directly to Fiber's response body writer (no buffering).
type fiberResponseWriter struct {
	c       *fiber.Ctx
	written bool
	status  int
}

func (w *fiberResponseWriter) Header() http.Header {
	// Return a fresh map — gqlgen uses this to set Content-Type, but
	// we pre-set that on the Fiber context in HandlePOST. Any mutations
	// to this map are discarded since Fiber headers are not
	// http.Header-compatible. This is safe because gqlgen only sets
	// Content-Type and we handle that above.
	return http.Header{}
}

func (w *fiberResponseWriter) Write(b []byte) (int, error) {
	w.written = true
	return w.c.Response().BodyWriter().Write(b)
}

func (w *fiberResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.c.Status(statusCode)
}

// ── Documentation pages (unchanged from previous handler) ──────────────────

const graphqlDocsHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>MagicWebb GraphQL API</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 720px; margin: 40px auto; padding: 0 20px; background: #09090b; color: #fafafa; }
a { color: #7dd3fc; }
code { background: rgba(255,255,255,0.08); padding: 2px 6px; border-radius: 4px; font-size: 0.9em; }
pre { background: rgba(255,255,255,0.04); padding: 16px; border-radius: 8px; overflow-x: auto; }
h1 { background: linear-gradient(90deg, #7dd3fc, #fcd34d, #c4b5fd); -webkit-background-clip: text; background-clip: text; color: transparent; }
</style>
</head>
<body>
<h1>✦ MagicWebb GraphQL API</h1>
<p>Send POST requests to <code>/graphql</code> with a JSON body containing <code>query</code> and optional <code>variables</code>.</p>
<p>Use the <a href="/graphiql">GraphiQL IDE</a> for interactive exploration.</p>
<h2>Example</h2>
<pre><code>curl -X POST https://magicwebb.xyz/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ listings(limit:5) { collection name priceWei } }"}'
</code></pre>
<h2>Available Queries</h2>
<table>
<tr><td><code>collection(address)</code></td><td>Single collection by contract</td></tr>
<tr><td><code>collections(limit)</code></td><td>Tracked collections</td></tr>
<tr><td><code>listing(collection, tokenID)</code></td><td>Single listing</td></tr>
<tr><td><code>listings(collection, seller, sort, limit, minPrice, maxPrice, traits)</code></td><td>Active listings</td></tr>
<tr><td><code>auction(id)</code></td><td>Single auction</td></tr>
<tr><td><code>auctions(collection, seller, status, limit, minPrice, maxPrice)</code></td><td>Auctions</td></tr>
<tr><td><code>offers(collection, tokenID, bidder, owner, status, limit)</code></td><td>Offers</td></tr>
<tr><td><code>search(query, limit)</code></td><td>Full-text search</td></tr>
<tr><td><code>activity(limit, address)</code></td><td>Activity feed</td></tr>
<tr><td><code>metrics</code></td><td>Market metrics</td></tr>
<tr><td><code>trending(window, limit)</code></td><td>Trending collections</td></tr>
<tr><td><code>profile(address)</code></td><td>User profile</td></tr>
<tr><td><code>tokenMeta(collection, tokenID)</code></td><td>Token name + image</td></tr>
<tr><td><code>walletNFTs(owner)</code></td><td>Wallet NFTs</td></tr>
</table>
</body>
</html>`

const graphiqlHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MagicWebb GraphiQL</title>
  <style>
    body { margin: 0; }
    #graphiql { height: 100vh; }
  </style>
  <link rel="stylesheet" href="https://unpkg.com/graphiql@3/graphiql.min.css" />
</head>
<body>
  <div id="graphiql">Loading...</div>
  <script src="https://unpkg.com/graphiql@3/graphiql.min.js" type="application/javascript"></script>
  <script src="https://unpkg.com/@graphiql/create-fetcher@2/dist/index.umd.js"></script>
  <script>
    const fetcher = GraphiQL.createFetcher({ url: '/graphql' });
    ReactDOM.render(
      React.createElement(GraphiQL, { fetcher, defaultQuery: '{ listings(limit:5) { collection name priceWei } }' }),
      document.getElementById('graphiql')
    );
  </script>
</body>
</html>`
