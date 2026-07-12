//go:build zigmedia

package media

/*
#cgo LDFLAGS: -L${SRCDIR}/../../zigsniff -lzignsniff
#include "../../zigsniff/zignsniff.h"
*/
import "C"
import "unsafe"

// sniffer detects the image format from raw bytes using the Zig-accelerated
// implementation via CGO. It replaces the Go-native magic-byte chain in
// SniffImage when built with `-tags zigmedia`.
//
// Build:
//
//	cd backend/zigsniff && zig build-lib -O ReleaseFast -dynamic zignsniff.zig
//	cd .. && go build -tags zigmedia ./cmd/server
//
// Returns the MIME type string and true when the body is a recognised image
// format. The Zig implementation processes the entire blob in a single pass
// with zero heap allocation.
//
//go:nocheckptr
func sniffer(body []byte) (mime string, ok bool) {
	if len(body) == 0 {
		return "", false
	}
	format := C.zig_sniff_image(
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(body))),
		C.size_t(len(body)),
	)
	if format == 0 {
		return "", false
	}
	var buf [16]byte
	C.zig_image_mime(C.uint(format), (*C.uint8_t)(unsafe.Pointer(&buf[0])))
	// Find null terminator
	end := 0
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return string(buf[:end]), true
}
