package main

import (
	"bytes"
	"html/template"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/OfficialA1manac/MagicWebb/frontend"
)

// docEntry describes one in-app document. Markdown sources are embedded in
// the binary (internal/ui/docs); rendering happens once, lazily, then is
// cached for the process lifetime.
type docEntry struct {
	Slug   string
	File   string
	Title  string
	Blurb  string
	Accent string // tailwind color stem for the index card + active nav state
	Icon   string
}

var docRegistry = []docEntry{
	{"whitepaper", "WHITEPAPER.md", "Whitepaper", "Vision, market, and the seller-pays economic model.", "emerald", "📜"},
	{"technical", "WHITEPAPER_TECHNICAL.md", "Technical Whitepaper", "Contracts, escrow flows, and the indexer pipeline in depth.", "sky", "⚙️"},
	{"user-guide", "USER_GUIDE.md", "User Guide", "Listing, bidding, offers, and withdrawals — step by step.", "violet", "🧭"},
	{"faq", "FAQ.md", "FAQ", "Quick answers: fees, refunds, wallets, and safety.", "amber", "💡"},
	{"token-hooks", "TOKEN_HOOKS.md", "Token Architecture", "Future token integration points anchored in the manager contract.", "rose", "🔗"},
}

var (
	docHTMLCache sync.Map // slug → template.HTML
	docMD        = goldmark.New(goldmark.WithExtensions(extension.GFM))
)

func findDoc(slug string) (docEntry, bool) {
	for _, d := range docRegistry {
		if d.Slug == slug {
			return d, true
		}
	}
	return docEntry{}, false
}

// renderDoc returns the cached HTML for a doc, rendering the embedded
// markdown on first access. Raw HTML in sources is escaped by goldmark's
// default policy; the sources are first-party but defense stays on.
func renderDoc(d docEntry) (template.HTML, error) {
	if h, ok := docHTMLCache.Load(d.Slug); ok {
		return h.(template.HTML), nil
	}
	src, err := frontend.FS.ReadFile("docs/" + d.File)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := docMD.Convert(src, &buf); err != nil {
		return "", err
	}
	h := template.HTML(buf.String())
	docHTMLCache.Store(d.Slug, h)
	return h, nil
}

func uiDocsIndex() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return render(c, "pages/docs.html", fiber.Map{
			"Title": "Docs",
			"Docs":  docRegistry,
		})
	}
}

func uiDoc() fiber.Handler {
	return func(c *fiber.Ctx) error {
		d, ok := findDoc(c.Params("slug"))
		if !ok {
			return c.Status(fiber.StatusNotFound).SendString("doc not found")
		}
		content, err := renderDoc(d)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("doc unavailable")
		}
		return render(c, "pages/docs.html", fiber.Map{
			"Title":   d.Title,
			"Docs":    docRegistry,
			"Active":  d.Slug,
			"Doc":     d,
			"Content": content,
		})
	}
}
