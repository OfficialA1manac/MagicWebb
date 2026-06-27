package api

import (
	"net/url"
	"strings"
)

// isValidWeiStr returns true when s is a non-negative integer consisting
// solely of digits (0-9), suitable for use as a wei value. Empty string
// and negative values are rejected.
func isValidWeiStr(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

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
