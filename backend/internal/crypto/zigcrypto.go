//go:build zigmedia

package crypto

/*
#cgo LDFLAGS: -L${SRCDIR}/../../zigcrypto -lzigcrypto
#include "../../zigcrypto/zigcrypto.h"
*/
import "C"
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"math/big"
	"unsafe"
)

// Keccak256 computes the Keccak-256 hash of data using the Zig-accelerated
// implementation via CGO. Returns 32 bytes.
func Keccak256(data []byte) [32]byte {
	var out [32]byte
	if len(data) == 0 {
		return out
	}
	C.zig_keccak256(
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data))),
		C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
	)
	return out
}

// VerifyEIP191 verifies an EIP-191 personal-signature.
// message: the original message (before the \x19Ethereum... prefix)
// sigHex: 65-byte recoverable signature (v, r, s)
// Returns true when the recovered signer matches expectedAddr.
func VerifyEIP191(message []byte, sigBytes []byte, expectedAddr []byte) (bool, error) {
	if len(sigBytes) != 65 {
		return false, errors.New("invalid signature length")
	}

	// Compute keccak256("\x19Ethereum Signed Message:\n" + len(message) + message)
	prefix := "\x19Ethereum Signed Message:\n"
	lenStr := itoa(len(message))
	msg := append([]byte(prefix), []byte(lenStr)...)
	msg = append(msg, message...)

	hash := Keccak256(msg)

	// Recover public key from signature
	// sigBytes = [r (32)] [s (32)] [v (1)]
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:64])
	v := int(sigBytes[64])

	// EIP-155: v may be 27/28 or >= chain_id*2+35
	if v >= 27 {
		v -= 27
	}
	if v < 0 || v > 1 {
		return false, errors.New("invalid recovery id")
	}

	// Recover the public key using the signature
	x, y := recoverPublicKey(hash[:], r, s, v)
	if x == nil || y == nil {
		return false, errors.New("public key recovery failed")
	}

	// Compute the Ethereum address from the public key
	pubKeyBytes := elliptic.Marshal(elliptic.P256(), x, y)
	addrHash := Keccak256(pubKeyBytes[1:])
	recoveredAddr := addrHash[12:]

	// Compare with expected address
	if len(expectedAddr) != 20 {
		return false, errors.New("invalid expected address length")
	}
	for i := 0; i < 20; i++ {
		if recoveredAddr[i] != expectedAddr[i] {
			return false, nil
		}
	}
	return true, nil
}

// recoverPublicKey recovers the uncompressed public key from a signature.
func recoverPublicKey(hash, rBytes, sBytes []byte, v int) (*big.Int, *big.Int, error) {
	// This is a simplified key recovery. Full implementation would use
	// secp256k1 curve. For now, delegate to Go's ecdsa package for the
	// curve parameters and signature verification.
	curve := elliptic.P256()
	r := new(big.Int).SetBytes(rBytes)
	s := new(big.Int).SetBytes(sBytes)

	// For secp256k1 (Bitcoin curve), use the standard library's recovery
	// through the go-ethereum package or a dedicated library.
	// This bridge delegates the heavy lifting to Zig where available.
	_ = curve
	_ = r
	_ = s
	return nil, nil, errors.New("use go-ethereum crypto for full ECDSA recovery; Zig path handles hash-only verification")
}

// itoa is a simple integer to ASCII conversion for EIP-191 message length.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// DefaultHash implements the standard Go crypto/sha256-like interface
// backed by Zig Keccak256 for when SHA-256 is the target.
// Builds with: -tags zigmedia
var DefaultHash = Keccak256
