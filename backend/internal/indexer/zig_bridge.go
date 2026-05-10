//go:build cgo && zigperf

package indexer

/*
#cgo CFLAGS:  -I${SRCDIR}/../../zig/zig-out/include
#cgo LDFLAGS: -L${SRCDIR}/../../zig/zig-out/lib -lwebbplace_perf
#include "webbplace_perf.h"
*/
import "C"
import "unsafe"

// BatchVerify calls into Zig if available; returns (ok, implemented).
func BatchVerify(blob []byte) (ok, implemented bool) {
	if len(blob) == 0 {
		return true, true
	}
	r := C.wb_batch_verify((*C.uint8_t)(unsafe.Pointer(&blob[0])), C.size_t(len(blob)))
	if r < 0 {
		return false, false
	}
	return r == 1, true
}
