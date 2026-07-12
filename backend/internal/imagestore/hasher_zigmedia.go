//go:build zigmedia

package imagestore

/*
#cgo LDFLAGS: -L${SRCDIR}/../../zigsha256 -lzigsha256
#include "../../zigsha256/zigsha256.h"
*/
import "C"
import (
	"crypto/sha256"
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
