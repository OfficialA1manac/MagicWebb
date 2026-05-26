// Package ui holds embedded templates and static assets.
package ui

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed all:templates all:static
var FS embed.FS

var funcMap = template.FuncMap{
	"wei2eth": func(wei string) string {
		if len(wei) == 0 {
			return "0"
		}
		if len(wei) <= 18 {
			return "0." + wei
		}
		whole := wei[:len(wei)-18]
		frac := wei[len(wei)-18 : len(wei)-14]
		return whole + "." + frac
	},
	"shortAddr": func(addr string) string {
		if len(addr) < 10 {
			return addr
		}
		return addr[:6] + "…" + addr[len(addr)-4:]
	},
}

var partialPaths = []string{
	"partials/listing_cards.html",
	"partials/auction_cards.html",
	"partials/activity_feed.html",
}

var pagePaths = []string{
	"pages/home.html",
	"pages/listings.html",
	"pages/auctions.html",
	"pages/auction.html",
	"pages/offers.html",
	"pages/profile.html",
	"pages/collection.html",
	"pages/token.html",
	"pages/search.html",
	"pages/metrics.html",
}

// Templates maps "pages/home.html" and "partials/listing_cards.html" etc.
// to parsed template sets. Each page gets its own set to avoid "content" conflicts.
var Templates map[string]*template.Template

func init() {
	sub, err := fs.Sub(FS, "templates")
	if err != nil {
		panic("ui: templates sub: " + err.Error())
	}

	Templates = make(map[string]*template.Template)

	// Per-page sets: layout + page + all partials
	for _, page := range pagePaths {
		files := make([]string, 0, 2+len(partialPaths))
		files = append(files, "layout.html", page)
		files = append(files, partialPaths...)
		t := template.Must(template.New("layout.html").Funcs(funcMap).ParseFS(sub, files...))
		Templates[page] = t
	}

	// Standalone partial sets (for HTMX partial swaps)
	for _, partial := range partialPaths {
		t := template.Must(template.New("").Funcs(funcMap).ParseFS(sub, partial))
		Templates[partial] = t
	}
}
