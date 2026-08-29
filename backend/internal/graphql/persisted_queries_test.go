package graphql

import (
	"testing"

	gqlparser "github.com/vektah/gqlparser/v2"
)

// TestPersistedQueriesValidateAgainstSchema validates every build-time
// registered persisted query against the executable schema. This catches
// field-name drift (e.g. auctionId vs auctionID) at CI time instead of as
// runtime validation errors for every client that sends the hash.
func TestPersistedQueriesValidateAgainstSchema(t *testing.T) {
	if len(persistedQueries) == 0 {
		t.Fatal("no persisted queries registered")
	}
	for hash, query := range persistedQueries {
		if _, errs := gqlparser.LoadQuery(parsedSchema, query); len(errs) > 0 {
			t.Errorf("persisted query %s does not validate against the schema:\n%v\nquery:\n%s", hash, errs, query)
		}
	}
}

// TestPrivatePersistedQueriesNotCDNCacheable asserts the user-scoped
// persisted queries are excluded from public CDN caching, and that public
// ones remain cacheable.
func TestPrivatePersistedQueriesNotCDNCacheable(t *testing.T) {
	if len(privatePersistedQueries) == 0 {
		t.Fatal("no private persisted queries registered — notifications/savedSearches must be private")
	}
	for hash := range privatePersistedQueries {
		if IsPersistedQueryCDNCacheable(hash) {
			t.Errorf("private persisted query %s reported as CDN-cacheable", hash)
		}
	}
	// Unknown hashes are never cacheable.
	if IsPersistedQueryCDNCacheable("deadbeef") {
		t.Error("unknown hash reported as CDN-cacheable")
	}
	// At least one public query must remain cacheable.
	public := 0
	for hash := range persistedQueries {
		if IsPersistedQueryCDNCacheable(hash) {
			public++
		}
	}
	if public == 0 {
		t.Error("no persisted query is CDN-cacheable — allowlist gating is broken")
	}
}
