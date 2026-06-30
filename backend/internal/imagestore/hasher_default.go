//go:build !zigmedia

package imagestore

import (
	"crypto/sha256"
)

// hashBytes computes the SHA-256 digest of body using Go's standard library.
// This is the default implementation used when the zigmedia build tag is NOT set.
// When built with `-tags zigmedia`, the Zig accelerated version is used instead.
func hashBytes(body []byte) [sha256.Size]byte {
	return sha256.Sum256(body)
}
