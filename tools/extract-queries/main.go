// extract-queries scans the project for GraphQL query strings in source
// files (.go, .js, .ts, .svelte, .astro, .html) and outputs register()
// calls for any queries not already registered in
// backend/internal/graphql/persisted_queries.go.
//
// Usage:
//
//	go run ./tools/extract-queries              # auto-detect project root via git
//	go run ./tools/extract-queries -root /path  # specify project root explicitly
//
// The output is valid Go code suitable for appending to the init() block
// in persisted_queries.go. Review the output before pasting — dynamically
// generated queries and fragments may need manual adjustment.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── Configuration ─────────────────────────────────────────────────────────

var (
	// Extensions scanned for GraphQL queries.
	scanExts = map[string]bool{
		".go":     true,
		".js":     true,
		".ts":     true,
		".tsx":    true,
		".svelte": true,
		".astro":  true,
		".html":   true,
	}

	// Directories excluded from scanning.
	excludeDirs = map[string]bool{
		"node_modules":          true,
		".git":                  true,
		"dist":                  true,
		"build":                 true,
		".astro":                true,
		"vendor":                true,
		"zig-cache":             true,
		"tools/extract-queries": true,
	}

	// Files excluded from scanning.
	excludeFiles = map[string]bool{
		"persisted_queries.go":   true,
		"generated.go":           true,
		"models_gen.go":          true,
		"subscription_gen.go":    true,
		"marketplace.pb.go":      true,
		"marketplace.connect.go": true,
		"cdn.min.js":             true,
	}

	// Path to the persisted queries registry file.
	persistedQueriesPath = "backend/internal/graphql/persisted_queries.go"
)

// backtickRe matches all backtick-delimited blocks in source text.
var backtickRe = regexp.MustCompile("`([^`]*)`")

// opPrefixRe matches the start of a GraphQL operation or fragment
// definition. The operation name is optional (`query { ... }` is a valid
// anonymous operation), and a document may open with a fragment that
// precedes its operation — \b keeps identifiers like `queryClient`
// from matching.
var opPrefixRe = regexp.MustCompile(`^(?:query|mutation|subscription|fragment)\b`)

// shorthandRe matches a shorthand query opening: { followed by a field name.
var shorthandRe = regexp.MustCompile(`^\s*\{\s*[a-zA-Z_]\w*`)

// ── Query extraction ──────────────────────────────────────────────────────

// extractQueries scans a file's content and returns deduplicated, normalized
// GraphQL query strings found in backtick-delimited blocks.
func extractQueries(content string) []string {
	seen := make(map[string]bool)
	var queries []string

	matches := backtickRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		block := strings.TrimSpace(m[1])
		if block == "" {
			continue
		}

		// Quick pre-check: looks like a GraphQL query?
		if !isGraphQLQuery(block) {
			continue
		}

		q := normalizeQuery(block)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		queries = append(queries, q)
	}

	return queries
}

// isGraphQLQuery performs a lightweight check on a backtick-delimited block
// to determine if it's a GraphQL query (vs. JSON, JS object, regex pattern,
// JS template literal, etc.). Uses multiple heuristics:
//
//   - Starts with query/mutation keyword
//   - Starts with { followed by a known field name pattern
//   - Does NOT contain JS template literal interpolation (${...})
func isGraphQLQuery(block string) bool {
	trimmed := strings.TrimSpace(block)

	// Must be reasonably long (at least a minimal query like "{ a }").
	if len(trimmed) < 3 {
		return false
	}

	// ── Reject non-GraphQL patterns ────────────────────────────────

	// JS template literals contain ${...} interpolation — GraphQL never does.
	if strings.Contains(trimmed, "${") {
		return false
	}

	// ── Positive signals ──────────────────────────────────────────

	// Operation keyword at start.
	if opPrefixRe.MatchString(trimmed) {
		return true
	}

	// Shorthand query: { followed by field(s).
	if shorthandRe.MatchString(trimmed) {
		inner := strings.TrimLeft(trimmed[1:], " \t") // strip leading { + whitespace
		return graphQLFieldRe.MatchString(inner)
	}

	return false
}

// graphQLFieldRe matches GraphQL field syntax inside a selection set:
//   fieldName              (plain field followed by whitespace/comma/})
//   fieldName(args)        (field with arguments)
//   fieldName: value       (alias)
//   fieldName {            (nested selection)
var graphQLFieldRe = regexp.MustCompile(`[a-zA-Z_]\w*\s*(?:\([^)]*\)|:\s*\S|\s*\{|(?:\s|,|\}|$))`)

// ── Query normalization ───────────────────────────────────────────────────

// normalizeQuery trims whitespace, normalizes indentation, and collapses
// blank lines. Skips obviously non-GraphQL content.
func normalizeQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Skip comment-like or non-query content.
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "#") {
		return ""
	}

	// Normalize whitespace: trim each line, skip blank lines.
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}

// ── File scanning ─────────────────────────────────────────────────────────

type queryEntry struct {
	query string
	hash  string
	file  string
}

func main() {
	rootFlag := flag.String("root", "", "project root directory (default: auto-detect from git root)")
	flag.Parse()

	root := *rootFlag
	if root == "" {
		root = detectGitRoot()
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "extract-queries: getwd: %v\n", err)
			os.Exit(1)
		}
	} else {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}

	// Step 1: Scan all files and extract queries.
	found := make(map[string]*queryEntry)

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			rel, _ := filepath.Rel(root, path)
			// Normalize to forward slashes for cross-platform matching.
			relSlash := filepath.ToSlash(rel)
			if excludeDirs[name] || excludeDirs[relSlash] || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if !scanExts[ext] {
			return nil
		}
		if excludeFiles[filepath.Base(path)] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		queries := extractQueries(string(content))
		for _, q := range queries {
			h := hashQuery(q)
			if _, exists := found[h]; !exists {
				rel, _ := filepath.Rel(root, path)
				found[h] = &queryEntry{query: q, hash: h, file: rel}
			}
		}
		return nil
	})

	// Step 2: Read existing persisted_queries.go to build the exclusion set.
	existing := readExistingQueries(root)

	// Step 3: Filter out already-registered queries.
	var newEntries []*queryEntry
	for h, e := range found {
		if existing[h] {
			continue
		}
		newEntries = append(newEntries, e)
	}

	// Step 4: Sort by hash for deterministic output.
	sort.Slice(newEntries, func(i, j int) bool {
		return newEntries[i].hash < newEntries[j].hash
	})

	// Step 5: Output Go code for new register() calls.
	if len(newEntries) == 0 {
		fmt.Println("// No new GraphQL queries found. All extracted queries are already registered.")
		return
	}

	fmt.Printf("// Auto-generated by tools/extract-queries. Found %d new query(s).\n", len(newEntries))
	fmt.Println("// Review each query and append to the init() block in")
	fmt.Println("// backend/internal/graphql/persisted_queries.go")
	fmt.Println()
	for _, e := range newEntries {
		fmt.Printf("\t// from %s\n", e.file)
		fmt.Printf("\t// hash: %s\n", e.hash)
		fmt.Printf("\tregister(`%s`)\n\n", e.query)
	}
}

// ── Existing query detection ──────────────────────────────────────────────

// readExistingQueries parses persisted_queries.go and returns the set of
// SHA-256 hashes already registered.
func readExistingQueries(root string) map[string]bool {
	existing := make(map[string]bool)
	path := filepath.Join(root, persistedQueriesPath)

	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract-queries: warning: cannot read %s: %v\n", persistedQueriesPath, err)
		return existing
	}

	registerRe := regexp.MustCompile("register\\(`([^`]*)`\\)")
	matches := registerRe.FindAllStringSubmatch(string(content), -1)
	for _, m := range matches {
		if len(m) >= 2 {
			q := normalizeQuery(m[1])
			if q != "" {
				existing[hashQuery(q)] = true
			}
		}
	}

	return existing
}

// ── Helpers ───────────────────────────────────────────────────────────────

// detectGitRoot walks up from the current directory until it finds a .git
// directory, returning the parent of .git (the project root). Returns ""
// when not found.
func detectGitRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hashQuery returns the hex-encoded SHA-256 of a normalized query string.
func hashQuery(q string) string {
	h := sha256.Sum256([]byte(q))
	return hex.EncodeToString(h[:])
}
