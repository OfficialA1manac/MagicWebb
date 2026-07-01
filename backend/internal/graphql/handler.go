package graphql

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// GraphQLServer handles GraphQL queries via the custom executor.
type GraphQLServer struct {
	Resolver QueryResolver
}

// NewGraphQLServer creates a GraphQL server with the db.Q as the data source.
func NewGraphQLServer(q *db.Q, bcast *sse.Broadcaster) *GraphQLServer {
	return &GraphQLServer{
		Resolver: &resolver{q: q, bcast: bcast},
	}
}

// HandlePOST is a Fiber handler for POST /graphql.
func (s *GraphQLServer) HandlePOST(c *fiber.Ctx) error {
	if c.Method() != http.MethodPost {
		return c.Status(fiber.StatusMethodNotAllowed).SendString("use POST for GraphQL queries")
	}

	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"errors": []map[string]any{
				{"message": "invalid JSON body"},
			},
		})
	}

	if req.Query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"errors": []map[string]any{
				{"message": "query is required"},
			},
		})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	data, err := Execute(ctx, s.Resolver, req.Query, req.Variables)
	if err != nil {
		// Log the full error internally for debugging; surface a sanitized
		// message to clients to avoid leaking internal details (DB queries,
		// table/column names, connection info) through GraphQL error responses.
		log.Printf("graphql execute error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"errors": []map[string]any{
				{"message": "internal server error"},
			},
		})
	}

	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

// HandleGET is a Fiber handler for GET /graphql that shows basic docs.
func (s *GraphQLServer) HandleGET(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(graphqlDocsHTML)
}

// HandleGraphiQL is a Fiber handler for GET /graphiql that shows the GraphiQL IDE.
func (s *GraphQLServer) HandleGraphiQL(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(graphiqlHTML)
}

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
