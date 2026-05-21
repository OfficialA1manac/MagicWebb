// Package hot exposes the Zig-compiled hot-path functions via CGo.
// Build: `make zig` in backend/ before `go build`.
package hot

/*
#cgo LDFLAGS: -L${SRCDIR}/lib -lwebbhot -lm
#include "hot.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"time"
	"unsafe"
)

// EventType mirrors the proto common.v1.EventType enum.
type EventType int32

const (
	EventUnknown         EventType = 0
	EventListed          EventType = 1
	EventDelisted        EventType = 2
	EventSale            EventType = 3
	EventAuctionCreated  EventType = 4
	EventBidPlaced       EventType = 5
	EventAuctionSettled  EventType = 6
	EventAuctionCancelled EventType = 7
	EventOfferAccepted   EventType = 8
)

// ScoreWeights configures the trending score formula.
type ScoreWeights struct {
	Views       float64
	Bids        float64
	Volume      float64
	DecayLambda float64 // decay per hour; ln(2)/14 ≈ half-life 14h
}

// DefaultWeights returns env-driven defaults (w1=0.3, w2=0.5, w3=0.2, λ=0.05).
func DefaultWeights() ScoreWeights {
	return ScoreWeights{Views: 0.3, Bids: 0.5, Volume: 0.2, DecayLambda: 0.05}
}

// DecodeLog matches topic0 against known Magic Webb selectors and decodes
// the ABI-encoded data into a JSON map. Returns (eventType, jsonBytes, error).
func DecodeLog(topic0 [32]byte, data []byte) (EventType, []byte, error) {
	const outCap = 4096
	outBuf := make([]byte, outCap)

	var evType C.wp_event_type_t
	var dataPtr *C.uint8_t
	if len(data) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}

	n := C.wp_decode_log(
		(*C.uint8_t)(unsafe.Pointer(&topic0[0])),
		dataPtr, C.size_t(len(data)),
		(*C.char)(unsafe.Pointer(&outBuf[0])), C.size_t(outCap),
		&evType,
	)
	if n == 0 {
		return EventUnknown, nil, fmt.Errorf("hot.DecodeLog: unrecognised topic or decode error")
	}
	return EventType(evType), outBuf[:n], nil
}

// Keccak256 computes keccak-256 via the Zig hot-path.
func Keccak256(data []byte) [32]byte {
	var out [32]byte
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	C.wp_keccak256(inPtr, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&out[0])))
	return out
}

// ComputeScore calculates the trending score for a collection window.
// volumeEth is the total volume denominated in ETH (not wei).
func ComputeScore(views, bids uint64, volumeEth float64, age time.Duration, w ScoreWeights) float64 {
	ageHours := age.Hours()
	return float64(C.wp_compute_score(
		C.uint64_t(views),
		C.uint64_t(bids),
		C.double(volumeEth),
		C.double(ageHours),
		C.wp_score_weights_t{
			w_views:       C.double(w.Views),
			w_bids:        C.double(w.Bids),
			w_volume:      C.double(w.Volume),
			decay_lambda:  C.double(w.DecayLambda),
		},
	))
}
