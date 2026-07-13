//go:build ignore

// Package generate provides go:generate directives for the Zig acceleration
// libraries. Run `go generate ./...` from the backend directory to compile
// all three Zig shared libraries with ReleaseFast optimizations.
//
// Prerequisites: zig 0.13.0 or later must be installed and on $PATH.
// The generated .so/.dll/.dylib files are gitignored and rebuilt by CI.
//
// Usage:
//
//	cd backend && go generate -tags zigmedia ./...

package generate

//go:generate zig build-lib -O ReleaseFast -dynamic ./zigsha256/zigsha256.zig
//go:generate zig build-lib -O ReleaseFast -dynamic ./zigcrypto/zigcrypto.zig
//go:generate zig build-lib -O ReleaseFast -dynamic ./zigsniff/zignsniff.zig
