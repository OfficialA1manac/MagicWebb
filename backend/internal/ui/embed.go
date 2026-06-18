// Package ui holds embedded templates and static assets.
package ui

import (
	"embed"
	"html/template"
	"io/fs"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

var (
	strconvI = strconv.Itoa
	formatFloat = func(f *big.Float, decimals int) string {
		return f.Text('f', decimals)
	}
)

//go:embed all:templates all:static all:docs
var FS embed.FS

var funcMap = template.FuncMap{
	// wei2flr converts a wei decimal string to a human-readable FLR string
	// with adaptive precision. The old padding-to-18-chars approach dropped
	// the leading "0" for sub-wei values (1 wei → ".0000") and silently
	// truncated values that overflowed 4 decimal places (e.g. 1.23456 FLR
	// rendered as "1.2345"). BigFloat arithmetic handles both correctly and
	// produces a clean, locale-independent output.
	"wei2flr": func(wei string) string {
		if wei == "" || wei == "0" {
			return "0.00"
		}
		w, ok := new(big.Int).SetString(wei, 10)
		if !ok {
			return "0.00"
		}
		bf := new(big.Float).SetPrec(64).SetInt(w)
		flr := new(big.Float).SetPrec(64).Quo(bf, big.NewFloat(1e18))
		return formatFloat(flr, 4)
	},
	// wei2flr6 keeps six decimals for high-precision contexts (auctions,
	// floor prices). Same BigFloat machinery — just more decimal places.
	"wei2flr6": func(wei string) string {
		if wei == "" || wei == "0" {
			return "0.000000"
		}
		w, ok := new(big.Int).SetString(wei, 10)
		if !ok {
			return "0.000000"
		}
		bf := new(big.Float).SetPrec(64).SetInt(w)
		flr := new(big.Float).SetPrec(64).Quo(bf, big.NewFloat(1e18))
		return formatFloat(flr, 6)
	},
	// wei2eth is the legacy alias retained for older templates / external
	// consumers. Delegates to the same BigFloat workflow as wei2flr so a
	// future precision tweak is a single-site change.
	"wei2eth": func(wei string) string {
		if wei == "" || wei == "0" {
			return "0.0000"
		}
		w, ok := new(big.Int).SetString(wei, 10)
		if !ok {
			return "0.0000"
		}
		bf := new(big.Float).SetPrec(64).SetInt(w)
		flr := new(big.Float).SetPrec(64).Quo(bf, big.NewFloat(1e18))
		return formatFloat(flr, 4)
	},
// mulf multiplies a numeric value (int or float64) by a float64 and renders
// it as a decimal string with adaptive precision. Drives template-side
// "bps → percent" conversions on auction detail (min increment %). Pure
// arithmetic — no wei scale, no half-up rounding (Go's strconv uses the
// shortest-round representation, good enough for a UI percentage).
"mulf": func(v any, factor float64) string {
	var x float64
	switch t := v.(type) {
	case int:
		x = float64(t)
	case int64:
		x = float64(t)
	case float64:
		x = t
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			x = f
		}
	default:
		x = 0
	}
	r := x * factor
	// Drop trailing zeros for a clean display: 5.00 → "5", 5.10 → "5.1".
	s := strconv.FormatFloat(r, 'f', -1, 64)
	return s
},
// subf returns (wei_a − wei_b) as a decimal-string wei value. The two
// arguments are wei decimal strings (PriceWei − NetOf(PriceWei) → the 1.5%
// fee line). BigFloat arithmetic so we never lose precision at the wei
// scale. Go-template funcMap cannot introspect nested arg types, so we
// accept strings here — caller-side just passes `{{subf .PriceWei (netOf
// .PriceWei)}}`.
"subf": func(aWei, bWei string) string {
	ai, okA := new(big.Int).SetString(aWei, 10)
	bi, okB := new(big.Int).SetString(bWei, 10)
	if !okA || !okB {
		return "0"
	}
	r := new(big.Int).Sub(ai, bi)
	return r.String()
},
// netOf returns the seller-pays net of a wei string: full price minus the
// immutable 1.5% platform fee, in wei (decimal string). Kept in template
// helpers so the price→rendered fee math lives in one place (the math
// itself is also encoded in the contract — they MUST stay equal).
"netOf": func(priceWei string) string {
		if priceWei == "" || priceWei == "0" {
			return "0"
		}
		w, ok := new(big.Int).SetString(priceWei, 10)
		if !ok {
			return "0"
		}
		fee := new(big.Int).Mul(w, big.NewInt(150))
		fee.Quo(fee, big.NewInt(10000))
		return new(big.Int).Sub(w, fee).String()
	},
	// pct formats a 0..1 ratio as a percentage string ("42%"). Edge cases:
	// negative or NaN inputs clamp to "0%"; overflows clamp to "100%".
	"pct": func(ratio float64) string {
		if ratio < 0 || ratio != ratio {
			return "0%"
		}
		if ratio > 1 {
			return "100%"
		}
		return strconvI(int(ratio*100)) + "%"
	},
	// shortNumber adds thousands separators to integer strings (e.g.
	// 1234567 → "1,234,567"). Used for supply, viewer, and bid counters.
	// Iterative (no recursion) — anon funcs in funcMap can't resolve
	// themselves by template name as a free identifier in Go.
	"shortNumber": func(n int64) string {
		neg := n < 0
		if neg {
			n = -n
		}
		s := strconvI(int(n))
		out := make([]byte, 0, len(s)+len(s)/3)
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, ',')
			}
			out = append(out, byte(c))
		}
		if neg {
			return "-" + string(out)
		}
		return string(out)
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
	"mediaURL": media.ProxyURL,
	// isUpstream reports whether a stored image_uri is an http(s)/(ipfs)
	// URL we still need to self-host — drives the user-triggerable retry
	// banner. Empty / local /api/v1/img/<sha> values are NOT upstream (the
	// slow-path worker has already cached them or the token never had a
	// usable image).
	"isUpstream": func(uri string) bool {
		return strings.HasPrefix(uri, "http://") ||
			strings.HasPrefix(uri, "https://") ||
			strings.HasPrefix(uri, "ipfs://")
	},
}

// partialPaths lists every partial HTML fragment that's loaded into BOTH
// the full-page templates AND standalone for HTMX partial swaps. Adding a
// new page-level partial here is the only step required to make it
// available on every page (it's included from layout.html via
// {{template "partials/<name>.html" .}}).
var partialPaths = []string{
	"partials/listing_cards.html",
	"partials/auction_cards.html",
	"partials/activity_feed.html",
	"partials/nft_picker.html",
	"partials/wc_qr_overlay.html",
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
	"pages/docs.html",
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
