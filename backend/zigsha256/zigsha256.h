#ifndef ZIGSHA256_H
#define ZIGSHA256_H

#include <stddef.h>
#include <stdint.h>

/// Computes SHA-256(data) and writes the 32-byte digest to `out`.
/// `out` must point to a writable buffer of at least 32 bytes.
void zig_sha256(const uint8_t* data, size_t len, uint8_t* out);

/// Returns the SHA-256 digest size in bytes (always 32).
unsigned int zig_sha256_digest_size(void);

#endif // ZIGSHA256_H
