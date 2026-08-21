package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
)

// mountAstro serves the Astro build from app/dist/ at the root URL prefix.
// It is the whole user-facing UI — the Go server renders no HTML itself.
//
// In dev the Astro dev server handles requests directly and proxies API calls
// to Go on :8080; in production this serves the pre-built static output, so no
// Node process runs alongside the binary.
//
// Anything with no matching file falls through to c.Next(), which now reaches
// only the API, auth, websocket and health routes.
//
// ASTRO_DIST_DIR overrides the path (default "../app/dist" for dev; the Docker
// image sets "/app/dist").
func mountAstro(app *fiber.App) {
	distPath := envOrDefault("ASTRO_DIST_DIR", "../app/dist")
	log.Info().Str("path", distPath).Msg("mounting Astro static pages at root /")

	// Custom middleware: serves Astro-built files from distPath when they
	// exist, otherwise calls c.Next() to pass through to the Go HTMX route
	// handlers. Uses fiber.Ctx.SendFile (fasthttp-native) to avoid the
	// net/http ↔ fasthttp type mismatch that http.FileServer would cause.
	app.Use("/", func(c *fiber.Ctx) error {
		path := c.Path()

		// Skip the filesystem stat for paths owned by dedicated handlers.
		if strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, "/auth/") ||
			path == "/healthz" ||
			path == "/readyz" {
			return c.Next()
		}

		// Bare /profile and /profile/ serve the Astro profile page
		// (profile/index.html). If no address is in the URL path,
		// the client-side JS shows a "Connect your wallet" prompt.
		// Previously these were hand-rolled to uiProfileRedirect
		// which 307'd to /listings when no session cookie was found.
		if path == "/profile" || path == "/profile/" {
			if idxPath := filepath.Join(distPath, "profile", "index.html"); fileExists(idxPath) {
				c.Set("Cache-Control", "public, max-age=300")
				return sendHTMLWithConfig(c, idxPath)
			}
			return c.Next()
		}

		// Normalise the path to a file path relative to the Astro dist dir.
		rel := strings.TrimPrefix(path, "/")
		if rel == "" {
			rel = "index.html"
		}

		// Sanitise the relative path to prevent directory traversal.
		// filepath.Join resolves ../ segments via filepath.Clean, so a
		// malicious /../../../etc/passwd would escape distPath. We enforce
		// that the resolved path stays under the Astro dist root.
		cleanRel := filepath.Clean(rel)
		fullPath := filepath.Join(distPath, cleanRel)
		cleanDist := filepath.Clean(distPath)
		if !strings.HasPrefix(fullPath, cleanDist+string(filepath.Separator)) && fullPath != cleanDist {
			// Path escapes the dist directory — reject and pass through.
			return c.Next()
		}

		// Try exact file match.
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			// HTML pages get a short cache (5 min) so deploys surface quickly.
			// Hashed JS/CSS assets (from Vite) get 1 year — they're immutable.
			if strings.HasSuffix(fullPath, ".html") {
				c.Set("Cache-Control", "public, max-age=300")
				return sendHTMLWithConfig(c, fullPath)
			} else {
				c.Set("Cache-Control", "public, max-age=31536000, immutable")
				return c.SendFile(fullPath)
			}
		}

		// Try directory index.html.
		indexPath := filepath.Join(distPath, cleanRel, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			// Redirect /listings → /listings/ so relative asset paths resolve.
			// Use 302 (temporary) so browsers don't cache the redirect — if
			// the site architecture changes, users won't be stuck in a loop.
			if !strings.HasSuffix(c.Path(), "/") {
				return c.Redirect(c.Path()+"/", fiber.StatusFound)
			}
			c.Set("Cache-Control", "public, max-age=300")
			return sendHTMLWithConfig(c, indexPath)
		}

		// Catch-all: Astro pages that use client-side URL parsing.
		// /token/* → token/index.html (JS parses addr + id from pathname)
		// /profile/:addr → profile/index.html (JS parses addr from pathname)
		// /auction/:id → auction/index.html (JS parses id from pathname)
		// /collection/:addr → collection/index.html (JS parses addr from pathname)
		var catchAlls = []struct{ prefix, dir string }{
			{"token/", "token"},
			{"profile/", "profile"},
			{"auction/", "auction"},
			{"collection/", "collection"},
			{"search/", "search"},
		}
		for _, ca := range catchAlls {
			if strings.HasPrefix(cleanRel, ca.prefix) && cleanRel != ca.dir {
				if idxPath := filepath.Join(distPath, ca.dir, "index.html"); fileExists(idxPath) {
					c.Set("Cache-Control", "public, max-age=300")
					return sendHTMLWithConfig(c, idxPath)
				}
			}
		}

		// Nothing built for this path — let the remaining routes try.
		return c.Next()
	})
}

// jsStringEscape makes a string safe for embedding inside a JavaScript
// single-quoted string literal. It escapes backslashes and single quotes
// — the two characters that could break out of the '...' context and
// enable script injection. Newlines are also escaped to keep the script
// on one line.
func jsStringEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", ``)
	// Escape < so that </script> in a config value cannot terminate
	// the script block early (HTML5 parser ends <script> on </script).
	s = strings.ReplaceAll(s, `<`, `\x3C`)
	return s
}

// astroConfigScript returns the <script> block injected into every Astro HTML
// response so the frontend's window.MW_* globals reflect the running
// server's chain config (RPC URL, chain ID, network name, native currency
// symbol, block explorer URL). Without this, Astro pages fall back to
// the hardcoded Coston2 defaults in BaseLayout.astro and the wallet UI
// shows the wrong chain / currency on mainnet deployments.
//
// The script overwrites the defaults set in BaseLayout.astro <head> ONLY
// if the server's config differs — the `||` fallback chain in the
// BaseLayout script runs first (SSR), then this block runs and replaces
// the values with the authoritative server config.
//
// All string values are jsStringEscape'd to prevent XSS via config values
// containing quotes or backslashes (defence-in-depth even though config
// is operator-controlled).
func astroConfigScript() string {
	return fmt.Sprintf(`<script>
window.MW_CHAIN_ID='%d';
window.MW_RPC_URL='%s';
window.MW_NETWORK_NAME='%s';
window.MW_NATIVE_CURRENCY='%s';
window.MW_EXPLORER='%s';
window.MW_WC_PROJECT_ID='%s';
window.MW_MARKETPLACE='%s';
window.MW_AUCTION='%s';
window.MW_OFFERBOOK='%s';
window.MW_NETWORK_URLS='%s';
</script>`,
		config.C.ChainID,
		jsStringEscape(config.C.RPCURL),
		jsStringEscape(config.C.NetworkName),
		jsStringEscape(config.C.NativeCurrency),
		jsStringEscape(config.C.ExplorerURL),
		jsStringEscape(config.C.WCProjectID),
		jsStringEscape(config.C.MarketplaceAddr),
		jsStringEscape(config.C.AuctionAddr),
		jsStringEscape(config.C.OfferBookAddr),
		jsStringEscape(networkURLs()),
	)
}

// networkURLs renders config.Networks back into the NETWORK_URLS wire format
// (chainID=origin,…) so the browser knows which sibling deployments exist —
// the wrong-network banner links to them, the switcher greys out the rest.
func networkURLs() string {
	parts := make([]string, 0, len(config.C.Networks))
	for _, n := range config.C.Networks {
		if n.URL != "" {
			parts = append(parts, fmt.Sprintf("%d=%s", n.ChainID, n.URL))
		}
	}
	return strings.Join(parts, ",")
}

// sendHTMLWithConfig serves an Astro-built HTML file with the server's
// chain config injected as a <script> block immediately before </head>.
// This is the Astro equivalent of the Go-template injection that
// render() does for HTMX pages — both paths ensure window.MW_* globals
// reflect the running server's chain (Coston2, Flare mainnet, Songbird)
// without rebuilding the Astro frontend.
//
// It also replaces <span class="mw-cur">C2FLR</span> placeholders with
// the actual native currency symbol server-side, eliminating the flash
// of the default "C2FLR" text that would otherwise appear before the
// client-side JS updater runs.
func sendHTMLWithConfig(c *fiber.Ctx, htmlPath string) error {
	// Check the cache — but validate that the file hasn't changed on disk
	// since it was cached. This handles Astro rebuilds during a rolling
	// deploy without requiring a process restart.
	if cached, ok := htmlCache.Load(htmlPath); ok {
		entry := cached.(htmlCacheEntry)
		if fi, err := os.Stat(htmlPath); err == nil && fi.ModTime().Equal(entry.modtime) {
			// File modtime matches cache timestamp — serve cached content.
			c.Set("Content-Type", "text/html; charset=utf-8")
			return c.SendString(entry.content)
		}
		// File has been updated (or Stat failed) — fall through to recompute.
	}

	body, err := os.ReadFile(htmlPath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("failed to read page")
	}
	content := string(body)

	// Grab the modtime AFTER reading the file (so the cache timestamp
	// is never older than the contents we cached). The stat is cheap
	// and runs only on cache miss.
	var modtime time.Time
	if fi, err := os.Stat(htmlPath); err == nil {
		modtime = fi.ModTime()
	}

	// Inject the config script just before </head>. The Astro-built
	// HTML always has a </head> tag (BaseLayout.astro guarantees it).
	// Using string replacement is safe because: (1) the script is
	// static per-process (computed once at init from config.C), and
	// (2) </head> appears exactly once in a well-formed HTML document.
	idx := strings.Index(content, "</head>")
	if idx < 0 {
		content = astroConfigScript() + content
	} else {
		content = content[:idx] + astroConfigScript() + content[idx:]
	}

	// Server-side replacement of .mw-cur span content so the correct
	// currency symbol renders immediately (no FOUC).
	curPlaceholder := `<span class="mw-cur">C2FLR</span>`
	curReplacement := `<span class="mw-cur">` + html.EscapeString(config.C.NativeCurrency) + `</span>`
	content = strings.ReplaceAll(content, curPlaceholder, curReplacement)
	// Also update .mw-net-name spans (used in the homepage testnet badge).
	netPlaceholder := `<span class="mw-net-name">Flare Coston2</span>`
	netReplacement := `<span class="mw-net-name">` + html.EscapeString(config.C.NetworkName) + `</span>`
	content = strings.ReplaceAll(content, netPlaceholder, netReplacement)

	// Store in cache before serving. The modtime snapshot ensures the
	// next request can detect if the file was touched by a deploy roll.
	htmlCache.Store(htmlPath, htmlCacheEntry{content: content, modtime: modtime})

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(content)
}

// htmlCache caches Astro HTML file content with server config injected.
// The astroConfigScript() and currency/network replacements are static per
// process (they only depend on config.C, which is immutable after Load()).
// Without caching, every uncached request re-reads the file from disk and
// re-runs strings.Index + 2× strings.ReplaceAll — avoidable disk I/O and
// string-alloc churn on the hot path. The cache is a sync.Map keyed by
// absolute path; entries store the processed content alongside the file's
// modification time at the time of caching. On lookup, the modtime is
// re-checked via os.Stat: if the file has changed (e.g. an Astro rebuild
// during a rolling deploy), the entry is invalidated and recomputed.
var htmlCache sync.Map

// htmlCacheEntry holds a cached HTML page together with the file modtime
// at the moment it was cached. On lookup, the caller must verify that
// the file's current modtime still matches entry.modtime — if not, the
// cache is stale and must be recomputed.
type htmlCacheEntry struct {
	content string
	modtime time.Time
}

// envOrDefault reads an env var, returning the default if empty or unset.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// fileExists returns true if the path is a regular file (not a directory).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
