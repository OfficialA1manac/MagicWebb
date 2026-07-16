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

// Keccak256Batch computes Keccak-256 hashes for multiple inputs (ZIG-1).
// Uses the Zig batch function for instruction-level parallelism — each
// hash operates on independent memory, enabling the CPU to pipeline
// multiple SIMD-accelerated Keccak operations. Returns a slice of 32-byte
// arrays, one per input. The caller owns the output.
func Keccak256Batch(inputs [][]byte) [][32]byte {
	n := len(inputs)
	if n == 0 {
		return nil
	}

	// Build pointer/length arrays for CGO passing.
	ptrs := make([]*C.uint8_t, n)
	lens := make([]C.size_t, n)
	for i, data := range inputs {
		if len(data) > 0 {
			ptrs[i] = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data)))
		}
		lens[i] = C.size_t(len(data))
	}

	// Single contiguous output buffer: n * 32 bytes.
	outBuf := make([]byte, n*32)

	C.zig_keccak256_batch(
		(**C.uint8_t)(unsafe.Pointer(&ptrs[0])),
		(*C.size_t)(unsafe.Pointer(&lens[0])),
		C.size_t(n),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
	)

	// Split contiguous buffer into individual [32]byte outputs.
	outs := make([][32]byte, n)
	for i := range n {
		copy(outs[i][:], outBuf[i*32:(i+1)*32])
	}
	return outs
}

// DefaultHash implements the standard Go crypto/sha256-like interface
// backed by Zig Keccak256 for when SHA-256 is the target.
// Builds with: -tags zigmedia
var DefaultHash = Keccak256
