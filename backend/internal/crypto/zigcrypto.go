//go:build zigmedia

package crypto

/*
#cgo LDFLAGS: -L${SRCDIR}/../../zigcrypto -lzigcrypto
#include "../../zigcrypto/zigcrypto.h"
*/
import "C"
import (
	"unsafe"
)

// Keccak256 computes the Keccak-256 hash of data using the Zig-accelerated
// implementation via CGO. Returns 32 bytes.
func Keccak256(data []byte) [32]byte {
	var out [32]byte
	C.zig_keccak256(
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data))),
		C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
	)
	return out
}

// DefaultHash implements the standard Go crypto/sha256-like interface
// backed by Zig Keccak256 for when SHA-256 is the target.
// Builds with: -tags zigmedia
var DefaultHash = Keccak256
