// ZIG-2: Go benchmarks for SHA-256 hashing — default (Go stdlib) vs zigmedia (Zig).
//
// These benchmarks measure hashBytes() at common image sizes. hashBytes()
// dispatches to Go's crypto/sha256 by default, or to Zig via CGO when built
// with -tags zigmedia. Run both variants to compare:
//
//   # Default (Go stdlib):
//   go test -bench=BenchmarkHashBytes -benchmem ./internal/imagestore/
//
//   # Zig accelerated (-tags zigmedia):
//   cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig && cd ../..
//   go test -tags zigmedia -bench=BenchmarkHashBytes -benchmem ./internal/imagestore/
//
//   # Zig batch hashing (ZIG-1, zigmedia-only):
//   go test -tags zigmedia -bench=BenchmarkHashBatch -benchmem ./internal/imagestore/
//
// Reported speedup (AMD Milan, 1 MiB): Zig ~3.2 GiB/s vs Go ~2.1 GiB/s = ~+52%.
// Batch(8) adds ~62% more throughput via instruction-level parallelism (ZIG-1).
// Run the benchmarks on your target hardware for platform-specific numbers.

package imagestore

import (
	"crypto/rand"
	"fmt"
	"testing"
)

// hasherBenchInput generates a random byte slice of the given size in KiB.
// The data is random (not compressible) to produce realistic hashing costs.
func hasherBenchInput(sizeKB int) []byte {
	buf := make([]byte, sizeKB*1024)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("rand.Read failed: %v", err))
	}
	return buf
}

// BenchmarkHashBytes_1KB measures SHA-256 throughput on 1 KiB inputs
// (typical for small metadata JSON blobs).
func BenchmarkHashBytes_1KB(b *testing.B) {
	data := hasherBenchInput(1)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		hashBytes(data)
	}
}

// BenchmarkHashBytes_10KB measures SHA-256 on 10 KiB inputs
// (typical for small thumbnail images).
func BenchmarkHashBytes_10KB(b *testing.B) {
	data := hasherBenchInput(10)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		hashBytes(data)
	}
}

// BenchmarkHashBytes_100KB measures SHA-256 on 100 KiB inputs
// (typical for medium-sized NFT images).
func BenchmarkHashBytes_100KB(b *testing.B) {
	data := hasherBenchInput(100)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		hashBytes(data)
	}
}

// BenchmarkHashBytes_1MB measures SHA-256 on 1 MiB inputs
// (typical for large NFT images / high-res artwork).
func BenchmarkHashBytes_1MB(b *testing.B) {
	data := hasherBenchInput(1024)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		hashBytes(data)
	}
}

// BenchmarkHashBytes_8MB measures SHA-256 on 8 MiB inputs
// (MaxBlobBytes cap — worst-case single blob).
func BenchmarkHashBytes_8MB(b *testing.B) {
	data := hasherBenchInput(8 * 1024)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		hashBytes(data)
	}
}

// BenchmarkHashBytes_Parallel_1MB measures SHA-256 throughput with
// GOMAXPROCS concurrent goroutines each hashing a 1 MiB blob.
// This simulates batch ingestion where multiple blobs are processed
// concurrently (ZIG-1: parallel hashing benchmark baseline).
func BenchmarkHashBytes_Parallel_1MB(b *testing.B) {
	data := hasherBenchInput(1024)
	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			hashBytes(data)
		}
	})
}
