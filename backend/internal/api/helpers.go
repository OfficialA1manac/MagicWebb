package api

import (
	"net/url"
	"strings"
)

// isAllowedScheme checks if a URI uses http or https scheme.
// Rejects javascript:, data:, vbscript:, and other dangerous schemes.
// Also rejects protocol-relative URLs (//evil.com) whose scheme
// resolves to "" — those would render as href="//evil.com", which
// inherits the current page's scheme at runtime.
func isAllowedScheme(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	s := strings.ToLower(parsed.Scheme)
	if s == "" && raw != "" {
		return false
	}
	return s == "http" || s == "https"
}
