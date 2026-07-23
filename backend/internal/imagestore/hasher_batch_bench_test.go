//go:build zigmedia

// ZIG-2: Benchmarks for the Zig-accelerated batch hash function.
//
// Since hashBatch is only defined when built with -tags zigmedia, these
// benchmarks live in a separate file with the zigmedia build constraint.
//
// Run with:
//   cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig && cd ../..
//   go test -tags zigmedia -bench=BenchmarkHashBatch -benchmem ./internal/imagestore/
//
// Compare against the single-hash benchmarks (which use hashBytes) to see
// the throughput improvement from batching (ZIG-1).

package imagestore

import (
	"crypto/sha256"
	"testing"
)

// BenchmarkHashBatch_8x100KB measures batch SHA-256 throughput on 8 blobs
// of 100 KiB each (800 KiB total). This simulates batch image ingestion
// where 8 medium-sized NFT images are processed in one call.
func BenchmarkHashBatch_8x100KB(b *testing.B) {
	const batchSize = 8
	bodies := make([][]byte, batchSize)
	for i := range batchSize {
		bodies[i] = hasherBenchInput(100) // 100 KiB each
	}

	b.ResetTimer()
	b.SetBytes(int64(100 * 1024 * batchSize))
	for i := 0; i < b.N; i++ {
		hashBatch(bodies)
	}
}

// BenchmarkHashBatch_4x1MB measures batch SHA-256 throughput on 4 blobs
// of 1 MiB each (4 MiB total). Typical for batch processing of large
// NFT artwork images.
func BenchmarkHashBatch_4x1MB(b *testing.B) {
	const batchSize = 4
	bodies := make([][]byte, batchSize)
	for i := range batchSize {
		bodies[i] = hasherBenchInput(1024) // 1 MiB each
	}

	b.ResetTimer()
	b.SetBytes(int64(1024 * 1024 * batchSize))
	for i := 0; i < b.N; i++ {
		hashBatch(bodies)
	}
}

// BenchmarkHashBatch_16x10KB measures batch SHA-256 throughput on 16 blobs
// of 10 KiB each. This simulates batch hashing of many small thumbnail
// or metadata blobs — a common pattern during re-indexing.
func BenchmarkHashBatch_16x10KB(b *testing.B) {
	const batchSize = 16
	bodies := make([][]byte, batchSize)
	for i := range batchSize {
		bodies[i] = hasherBenchInput(10) // 10 KiB each
	}

	b.ResetTimer()
	b.SetBytes(int64(10 * 1024 * batchSize))
	for i := 0; i < b.N; i++ {
		hashBatch(bodies)
	}
}

// TestHashBatch_Correctness verifies that hashBatch produces the same
// results as sequential hashBytes calls for a fixed set of inputs.
// This validates the CGO bridge wiring between Go and Zig.
func TestHashBatch_Correctness(t *testing.T) {
	bodies := [][]byte{
		[]byte("hello world"),
		[]byte(""),
		[]byte("The quick brown fox jumps over the lazy dog"),
		[]byte(string(make([]byte, 1024))), // 1 KiB of zeros
	}

	// Compute expected results using sequential hashBytes.
	expected := make([][sha256.Size]byte, len(bodies))
	for i, body := range bodies {
		expected[i] = hashBytes(body)
	}

	results := hashBatch(bodies)
	for j := range bodies {
		if results[j] != expected[j] {
			t.Errorf("hashBatch result %d mismatch: got %x, want %x", j, results[j], expected[j])
		}
	}
}
