#ifndef ZIGSHA256_H
#define ZIGSHA256_H

#include <stddef.h>
#include <stdint.h>

/// Computes SHA-256(data) and writes the 32-byte digest to `out`.
/// `out` must point to a writable buffer of at least 32 bytes.
void zig_sha256(const uint8_t* data, size_t len, uint8_t* out);

/// Returns the SHA-256 digest size in bytes (always 32).
unsigned int zig_sha256_digest_size(void);

// ── ZIG-1: SIMD batch hashing ────────────────────────────────────────────────

/// Computes SHA-256 for `count` inputs in parallel (instruction-level
/// parallelism via independent hash states). `data_ptrs` is an array of
/// `count` pointers; `data_lens` is an array of `count` lengths; `outs`
/// is a contiguous buffer of `count * 32` bytes. All arrays must be
/// pre-allocated by the caller.
void zig_sha256_batch(
    const uint8_t* const* data_ptrs,
    const size_t* data_lens,
    size_t count,
    uint8_t* outs
);

#endif // ZIGSHA256_H
