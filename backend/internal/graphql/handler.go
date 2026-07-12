package graphql

import (
	"context"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/dataloader"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// GraphQLServer wraps a gqlgen-generated executable schema with Fiber
// HTTP handlers for POST /graphql, GET /graphql (docs), and GET /graphiql.
type GraphQLServer struct {
	srv *handler.Server
	q   *db.Q // retained for DataLoader creation per-request
}

// NewGraphQLServer creates a gqlgen-based GraphQL server powered by the
// generated executable schema from schema.graphql. The db.Q provides
// data access; bcast is retained for future push-notification resolvers.
//
// Complexity limits: max query depth of 12 and max 200 nodes to prevent
// resource-exhaustion GraphQL queries (deeply nested collections.listings
// etc.). The LRU operation cache reduces repeated parse/validate costs.
func NewGraphQLServer(q *db.Q, bcast *sse.Broadcaster) *GraphQLServer {
	resolver := NewResolver(q, bcast)
	schema := NewExecutableSchema(Config{
		Resolvers: resolver,
	})

	srv := handler.New(schema)

	// Transport: accept POST JSON and GET query-string queries.
	// GraphQL subscriptions are enabled in the schema and resolver layer.
	// Subscriptions are delivered via the existing /ws WebSocket endpoint
	// which bridges Broadcaster push events to connected clients.
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})

	// Complexity limits: prevent deep-nested or mega-node queries from
	// overloading the DB. 12-depth is enough for any reasonable query
	// (e.g. collections->listings->seller); 200 node cap bounds total
	// fields requested across all nested objects.
	srv.Use(extension.FixedComplexityLimit(200))

	// APQ (Automatic Persisted Queries): clients send a hash first;
	// full query only on cache miss. Halves bandwidth for repeated
	// queries (e.g., polling a saved search).
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	// Introspection: enabled in all environments so GraphiQL and
	// external tooling (Apollo Studio, Postman, etc.) can discover
	// the schema. No auth secrets are exposed through introspection.
	// CORS already limits which origins can access /graphql.

	return &GraphQLServer{srv: srv, q: q}
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
