//go:build !cgo || !zigperf

package indexer

// BatchVerify pure-Go fallback when Zig static lib isn't built or CGo disabled.
// Always reports "not implemented" so callers verify per-signature with go-ethereum.
func BatchVerify(_ []byte) (ok, implemented bool) { return false, false }
