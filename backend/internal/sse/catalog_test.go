package sse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each transport's source must mention exactly the event types the catalog
// says it carries. Source-string checks are deliberately crude: they catch
// the drift that matters (a new event wired into one face and forgotten in
// another) without coupling the test to each package's internals.
func TestCatalogParityAcrossTransports(t *testing.T) {
	root := filepath.Join("..")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	wsSrc := read("ws/message.go")
	gqlSrc := read("graphql/resolver.go")
	cnSrc := read("connectrpc/marketplacev1/subscription_stream.go")
	whSrc := read("webhook/dispatcher.go")

	for _, e := range EventCatalog {
		q := `"` + e.Type + `"`
		check := func(face string, want bool, src string) {
			got := strings.Contains(src, q)
			if got != want {
				t.Errorf("%s: catalog says %s=%v but source %s it", e.Type, face, want, map[bool]string{true: "mentions", false: "does not mention"}[got])
			}
		}
		check("WS", e.WS, wsSrc)
		check("GraphQL", e.GraphQL, gqlSrc)
		check("Connect", e.Connect, cnSrc)
		check("Webhook", e.Webhook, whSrc)
	}
}

func TestCatalogChannelsAreKnown(t *testing.T) {
	known := map[string]bool{"token": true, "collection": true, "user": true, "tx": true, "activity": true}
	for _, e := range EventCatalog {
		for _, c := range e.Channels {
			if !known[c] {
				t.Errorf("%s: unknown channel %q (see ws/subscriptions.go)", e.Type, c)
			}
		}
	}
	if _, ok := CatalogEntryFor("tx-indexed"); !ok {
		t.Fatal("tx-indexed missing from catalog")
	}
}
