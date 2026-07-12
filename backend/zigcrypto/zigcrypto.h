#ifndef ZIGCRYPTO_H
#define ZIGCRYPTO_H

#include <stddef.h>
#include <stdint.h>

/// Computes Keccak-256(data) and writes the 32-byte digest to `out`.
/// `out` must point to a writable buffer of at least 32 bytes.
void zig_keccak256(const uint8_t* data, size_t len, uint8_t* out);

/// Returns the Keccak-256 digest size in bytes (always 32).
unsigned int zig_keccak256_digest_size(void);

/// Verifies an ECDSA secp256k1 signature.
/// Returns 1 on valid, 0 on invalid.
/// hash: 32-byte keccak256 hash
/// sig_r: 32-byte R component of signature
/// sig_s: 32-byte S component of signature
/// pub_x: 32-byte X coordinate of public key
/// pub_y: 32-byte Y coordinate of public key
int zig_ecdsa_verify(
    const uint8_t* hash,
    const uint8_t* sig_r,
    const uint8_t* sig_s,
    const uint8_t* pub_x,
    const uint8_t* pub_y
);

#endif // ZIGCRYPTO_H
