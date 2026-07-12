//go:build !zigmedia

package media

// sniffer returns false — Go-native SniffImage handles detection inline.
// This file is only compiled without the zigmedia build tag. When built
// with `-tags zigmedia`, the Zig-accelerated version is used instead
// (see zignsniff_zigmedia.go).
func sniffer(body []byte) (mime string, ok bool) {
	return "", false // defer to SniffImage's inline magic-byte switch
}
