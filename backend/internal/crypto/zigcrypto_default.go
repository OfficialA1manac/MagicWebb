//go:build !zigmedia

package crypto

import (
	"github.com/ethereum/go-ethereum/crypto"
)

// Keccak256 computes the Keccak-256 hash of data using go-ethereum.
func Keccak256(data []byte) [32]byte {
	return crypto.Keccak256Hash(data)
}

// DefaultHash uses go-ethereum's Keccak256 as the default hash function.
var DefaultHash = Keccak256
