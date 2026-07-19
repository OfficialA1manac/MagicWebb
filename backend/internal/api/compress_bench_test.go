package api

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

// ── Benchmark payloads ─────────────────────────────────────────────────

// listingPageHTML generates a realistic ~18 KB listing page payload with
// 48 NFT cards, matching a typical production page load.
func listingPageHTML() string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>MagicWebb - NFT Marketplace</title><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{font-family:system-ui,sans-serif;background:#09090b;color:#fafafa;margin:0;padding:0}.container{max-width:1200px;margin:0 auto;padding:16px}.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:16px}.card{background:rgba(255,255,255,0.04);border-radius:12px;overflow:hidden;border:1px solid rgba(255,255,255,0.06)}.card-img{width:100%;aspect-ratio:1;object-fit:cover}.card-body{padding:12px}.card-title{font-size:14px;font-weight:600;margin:0}.card-price{font-size:14px;font-weight:700;color:#7dd3fc}</style></head><body><header><nav><a href="/">✦ MagicWebb</a></nav></header><div class="container"><h1>Active Listings</h1><div class="grid">`)
	for i := 0; i < 48; i++ {
		b.WriteString(`<div class="card"><img class="card-img" src="/api/v1/img/abcdef0123456789abcdef01`)
		b.WriteByte(byte('0' + i%10))
		b.WriteString(`" alt="NFT"><div class="card-body"><p class="card-title">NFT Collection Item #`)
		b.WriteByte(byte('0' + (i/10)%10))
		b.WriteByte(byte('0' + i%10))
		b.WriteString(` - Rare Edition with Descriptive Name</p><p class="card-price">`)
		b.WriteByte(byte('0' + (i%5) + 1))
		b.WriteString(`.5 FLR</p></div></div>`)
	}
	b.WriteString(`</div></div><footer>MagicWebb NFT Marketplace</footer></body></html>`)
	return b.String()
}

// metricsJSON returns a realistic ~8 KB JSON API response with 20 listings.
func metricsJSON() string {
	var b strings.Builder
	b.WriteString(`{"listings":[`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"collection":"0x1234567890abcdef1234567890abcdef12345678","tokenId":"`)
		b.WriteByte(byte('0' + (i/10)%10))
		b.WriteByte(byte('0' + i%10))
		b.WriteString(`","name":"NFT #`)
		b.WriteByte(byte('0' + (i/10)%10))
		b.WriteByte(byte('0' + i%10))
		b.WriteString(`","priceWei":"1000000000000000000","seller":"0xabcdef1234567890abcdef1234567890abcdef12","attributes":{"Background":"Cosmic","Eyes":"Laser","Rarity":"Legendary"},"stats":{"volume24h":"5000000000000000000","floorPrice":"1000000000000000000"}}`)
	}
	b.WriteString(`],"total":20,"page":1,"hasMore":true}`)
	return b.String()
}

// ── Helpers ────────────────────────────────────────────────────────────

// compressGzip compresses data with gzip at the given level and returns
// the compressed size. Uses compress/gzip — the same library Fiber uses.
func compressGzip(b *testing.B, data []byte, level int) int {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		b.Fatalf("gzip.NewWriterLevel(%d): %v", level, err)
	}
	if _, err := w.Write(data); err != nil {
		b.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		b.Fatalf("gzip close: %v", err)
	}
	return buf.Len()
}

// compressBrotli compresses data with brotli at the given quality level
// and returns the compressed size. Uses andybalholm/brotli — the same
// library Fiber uses (v1.1.1).
func compressBrotli(b *testing.B, data []byte, quality int) int {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, quality)
	if _, err := w.Write(data); err != nil {
		b.Fatalf("brotli write: %v", err)
	}
	if err := w.Close(); err != nil {
		b.Fatalf("brotli close: %v", err)
	}
	return buf.Len()
}

// ── Benchmarks ─────────────────────────────────────────────────────────

func BenchmarkCompressGzipHTML(b *testing.B) {
	data := []byte(listingPageHTML())
	b.ReportAllocs()
	b.ResetTimer()

	var compressedSize int
	for i := 0; i < b.N; i++ {
		compressedSize = compressGzip(b, data, 5)
	}
	b.ReportMetric(float64(compressedSize), "compressed-bytes")
}

func BenchmarkCompressBrotliHTML(b *testing.B) {
	data := []byte(listingPageHTML())
	b.ReportAllocs()
	b.ResetTimer()

	var compressedSize int
	for i := 0; i < b.N; i++ {
		compressedSize = compressBrotli(b, data, 5)
	}
	b.ReportMetric(float64(compressedSize), "compressed-bytes")
}

func BenchmarkCompressGzipJSON(b *testing.B) {
	data := []byte(metricsJSON())
	b.ReportAllocs()
	b.ResetTimer()

	var compressedSize int
	for i := 0; i < b.N; i++ {
		compressedSize = compressGzip(b, data, 5)
	}
	b.ReportMetric(float64(compressedSize), "compressed-bytes")
}

func BenchmarkCompressBrotliJSON(b *testing.B) {
	data := []byte(metricsJSON())
	b.ReportAllocs()
	b.ResetTimer()

	var compressedSize int
	for i := 0; i < b.N; i++ {
		compressedSize = compressBrotli(b, data, 5)
	}
	b.ReportMetric(float64(compressedSize), "compressed-bytes")
}

// BenchmarkCompressCompare runs both gzip and brotli on HTML to produce
// a side-by-side comparison in a single benchmark output.
func BenchmarkCompressCompare(b *testing.B) {
	html := []byte(listingPageHTML())
	jsonData := []byte(metricsJSON())

	b.Run("HTML-gzip-5", func(b *testing.B) {
		b.ReportAllocs()
		var sz int
		for i := 0; i < b.N; i++ {
			sz = compressGzip(b, html, 5)
		}
		b.ReportMetric(float64(sz), "compressed-bytes")
		b.ReportMetric(float64(len(html)), "uncompressed-bytes")
	})
	b.Run("HTML-brotli-5", func(b *testing.B) {
		b.ReportAllocs()
		var sz int
		for i := 0; i < b.N; i++ {
			sz = compressBrotli(b, html, 5)
		}
		b.ReportMetric(float64(sz), "compressed-bytes")
		b.ReportMetric(float64(len(html)), "uncompressed-bytes")
	})
	b.Run("JSON-gzip-5", func(b *testing.B) {
		b.ReportAllocs()
		var sz int
		for i := 0; i < b.N; i++ {
			sz = compressGzip(b, jsonData, 5)
		}
		b.ReportMetric(float64(sz), "compressed-bytes")
		b.ReportMetric(float64(len(jsonData)), "uncompressed-bytes")
	})
	b.Run("JSON-brotli-5", func(b *testing.B) {
		b.ReportAllocs()
		var sz int
		for i := 0; i < b.N; i++ {
			sz = compressBrotli(b, jsonData, 5)
		}
		b.ReportMetric(float64(sz), "compressed-bytes")
		b.ReportMetric(float64(len(jsonData)), "uncompressed-bytes")
	})
}

// ── Size-only benchmarks (no timing — just compression ratios) ─────────

func TestCompressSizeComparison(t *testing.T) {
	html := []byte(listingPageHTML())
	jsonData := []byte(metricsJSON())

	// Compute sizes once.
	gzHTML := compressGzipBench(html, 5)
	brHTML := compressBrotliBench(html, 5)
	gzJSON := compressGzipBench(jsonData, 5)
	brJSON := compressBrotliBench(jsonData, 5)

	t.Logf("=== Compression Size Comparison (level 5) ===")
	t.Logf("HTML: uncompressed=%d B", len(html))
	t.Logf("  gzip -5:   %d B (%.1f%% of original)", gzHTML, float64(gzHTML)*100/float64(len(html)))
	t.Logf("  brotli -5: %d B (%.1f%% of original)", brHTML, float64(brHTML)*100/float64(len(html)))
	if gzHTML > 0 {
		t.Logf("  brotli saves %.1f%% over gzip", float64(gzHTML-brHTML)*100/float64(gzHTML))
	}

	t.Logf("JSON: uncompressed=%d B", len(jsonData))
	t.Logf("  gzip -5:   %d B (%.1f%% of original)", gzJSON, float64(gzJSON)*100/float64(len(jsonData)))
	t.Logf("  brotli -5: %d B (%.1f%% of original)", brJSON, float64(brJSON)*100/float64(len(jsonData)))
	if gzJSON > 0 {
		t.Logf("  brotli saves %.1f%% over gzip", float64(gzJSON-brJSON)*100/float64(gzJSON))
	}
}

// compressGzipBench compresses data with gzip at the given level using
// a discard writer. Returns compressed size; panics on error (acceptable
// for test usage since these are well-known inputs).
func compressGzipBench(data []byte, level int) int {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		panic("gzip.NewWriterLevel: " + err.Error())
	}
	if _, err := w.Write(data); err != nil {
		panic("gzip write: " + err.Error())
	}
	if err := w.Close(); err != nil {
		panic("gzip close: " + err.Error())
	}
	return buf.Len()
}

// compressBrotliBench compresses data with brotli at the given quality
// level using a discard writer. Returns compressed size; panics on error
// (acceptable for test usage since these are well-known inputs).
func compressBrotliBench(data []byte, quality int) int {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, quality)
	if _, err := w.Write(data); err != nil {
		panic("brotli write: " + err.Error())
	}
	if err := w.Close(); err != nil {
		panic("brotli close: " + err.Error())
	}
	return buf.Len()
}
