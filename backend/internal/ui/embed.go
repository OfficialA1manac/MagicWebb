// Package ui holds embedded templates and static assets.
package ui

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"
	"time"
)

//go:embed all:templates all:static
var FS embed.FS

var funcMap = template.FuncMap{
	// wei2eth converts a wei string to a 4-decimal ETH string.
	// Handles leading zeros correctly for sub-1-ether values.
	"wei2eth": func(wei string) string {
		if wei == "" || wei == "0" {
			return "0.0000"
		}
		// Pad to at least 19 chars (18 decimals + 1 integer digit minimum)
		for len(wei) <= 18 {
			wei = "0" + wei
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
	"lower": func(s string) string {
		return strings.ToLower(s)
	},
	"unix": func(t time.Time) int64 {
		return t.Unix()
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

// Templates maps page/partial keys to parsed template sets.
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
