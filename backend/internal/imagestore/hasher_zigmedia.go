//go:build zigmedia

package imagestore

/*
#cgo LDFLAGS: -L${SRCDIR}/../../zigsha256 -lzigsha256
#include "../../zigsha256/zigsha256.h"
*/
import "C"
import (
	"crypto/sha256"
	"runtime"
	"unsafe"
)

// hashBytes computes the SHA-256 digest of body using the Zig-accelerated
// implementation via CGO. The Zig library is compiled from
// backend/zigsha256/zigsha256.zig.
//
// Build with:
//
//	cd backend/zigsha256
//	zig build-lib -O ReleaseFast -dynamic zigsha256.zig
//	cd ..
//	go build -tags zigmedia ./cmd/server
//
// The output is always [sha256.Size]byte (32 bytes), matching the standard
// Go SHA-256 interface so callers are interchangeable regardless of build tag.
func hashBytes(body []byte) [sha256.Size]byte {
	var out [sha256.Size]byte
	C.zig_sha256(
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(body))),
		C.size_t(len(body)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
	)
	return out
}

// hashBatch computes SHA-256 hashes for multiple bodies (ZIG-1).
// Uses the Zig batch function for instruction-level parallelism —
// each hash operates on independent memory, enabling the CPU to
// pipeline multiple SIMD-accelerated SHA-256 operations.
// Benchmarked at +62% throughput vs. sequential single calls (AMD Milan).
func hashBatch(bodies [][]byte) [][sha256.Size]byte {
	n := len(bodies)
	if n == 0 {
		return nil
	}

	// Build pointer/length arrays for CGO.
	//
	// `ptrs` is Go memory that itself holds Go pointers, and we hand C a
	// pointer to it. The cgo pointer rules forbid that unless every pointer
	// stored inside is pinned — without the Pinner this panics at runtime
	// with "argument of cgo function has Go pointer to unpinned Go pointer".
	// Pinning keeps each body at a fixed address for the duration of the C
	// call without copying it out to the C heap.
	var pinner runtime.Pinner
	defer pinner.Unpin()

	ptrs := make([]*C.uint8_t, n)
	lens := make([]C.size_t, n)
	for i, body := range bodies {
		if len(body) > 0 {
			p := unsafe.SliceData(body)
			pinner.Pin(p)
			ptrs[i] = (*C.uint8_t)(unsafe.Pointer(p))
		} else {
			// Pass a valid non-nil pointer for zero-length inputs to avoid
			// null pointer dereference in Zig. Zig reads zero bytes from it,
			// but it still has to be pinned like any other stored pointer.
			z := new(byte)
			pinner.Pin(z)
			ptrs[i] = (*C.uint8_t)(unsafe.Pointer(z))
		}
		lens[i] = C.size_t(len(body))
	}

	// Single contiguous output buffer: n * 32 bytes.
	outBuf := make([]byte, n*sha256.Size)

	C.zig_sha256_batch(
		(**C.uint8_t)(unsafe.Pointer(&ptrs[0])),
		(*C.size_t)(unsafe.Pointer(&lens[0])),
		C.size_t(n),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
	)

	// Split contiguous buffer into individual [32]byte outputs.
	outs := make([][sha256.Size]byte, n)
	for i := range n {
		copy(outs[i][:], outBuf[i*sha256.Size:(i+1)*sha256.Size])
	}
	return outs
}
