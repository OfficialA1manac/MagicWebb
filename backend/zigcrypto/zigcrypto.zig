//! Zig Keccak256 and ECDSA verification using the standard library.
//! Compile to a static library for linking via CGO:
//!   zig build-lib -O ReleaseFast -dynamic zigcrypto.zig
//!
//! Exports:
//!   zig_keccak256(data, len, out) — Keccak-256 hash
//!   zig_ecdsa_verify(hash, sig_r, sig_s, pub_x, pub_y) — secp256k1 verify

const std = @import("std");
const crypto = std.crypto;
const ecdsa = crypto.sign.ecdsa;
const secp256k1 = crypto.ecc.Secp256k1;

/// Computes Keccak-256(data) and writes the 32-byte digest to `out`.
/// `out` must point to a writable buffer of at least 32 bytes.
export fn zig_keccak256(data: [*]const u8, len: usize, out: [*]u8) callconv(.C) void {
    var hash: [32]u8 = undefined;
    crypto.hash.sha3.Keccak256.hash(data[0..len], &hash, .{});
    @memcpy(out[0..32], hash[0..]);
}

/// Returns the Keccak-256 digest size in bytes (always 32).
export fn zig_keccak256_digest_size() callconv(.C) c_uint {
    return 32;
}

/// Verifies an ECDSA secp256k1 signature.
/// Returns 1 on valid, 0 on invalid.
/// hash: 32-byte keccak256 hash
/// sig_r: 32-byte R component of signature
/// sig_s: 32-byte S component of signature
/// pub_x: 32-byte X coordinate of public key
/// pub_y: 32-byte Y coordinate of public key
export fn zig_ecdsa_verify(
    hash: [*]const u8,
    sig_r: [*]const u8,
    sig_s: [*]const u8,
    pub_x: [*]const u8,
    pub_y: [*]const u8,
) callconv(.C) c_int {
    // Create signature from R||S
    var sig_bytes: [64]u8 = undefined;
    @memcpy(sig_bytes[0..32], sig_r[0..32]);
    @memcpy(sig_bytes[32..64], sig_s[0..32]);

    // Try to recover public key from signature
    // secp256k1 recoverable signature (65 bytes: 1 byte recovery + 32 R + 32 S)
    // Zig's std.crypto.sign.ecdsa.Ecdsa(P256, Sha256) doesn't directly support
    // Ethereum signatures. We use the lower-level curve operations instead.
    // For Ethereum ECDSA verification we need to verify against the provided
    // public key directly.
    
    // Parse the public key point
    var pub = secp256k1.PublicKey.fromSec1(pub_x[0..64]) catch {
        return 0;
    };
    
    // Create signature
    var sig = ecdsa.EcdsaSignature.fromBytes(sig_bytes, .{}) catch {
        return 0;
    };
    
    // Verify
    // Note: Ethereum uses Keccak256 not SHA256, so we verify against the
    // pre-hashed message
    _ = hash; // hash is the pre-computed Keccak256 hash
    
    // For now, this is a placeholder that verifies against the standard
    // ECDSA verification path. Ethereum uses personalized signing
    // (EIP-191: \x19Ethereum Signed Message:\n...) which needs the
    // message to be pre-hashed before calling this function.
    sig.verify(&pub, hash[0..32], .{}) catch {
        return 0;
    };
    return 1;
}

test "zig_keccak256 produces correct digest" {
    const testing = std.testing;

    // Empty string test
    var out1: [32]u8 = undefined;
    zig_keccak256("", 0, &out1);
    // Keccak-256("") = c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470
    const expected1 = [_]u8{
        0xc5, 0xd2, 0x46, 0x01, 0x86, 0xf7, 0x23, 0x3c,
        0x92, 0x7e, 0x7d, 0xb2, 0xdc, 0xc7, 0x03, 0xc0,
        0xe5, 0x00, 0xb6, 0x53, 0xca, 0x82, 0x27, 0x3b,
        0x7b, 0xfa, 0xd8, 0x04, 0x5d, 0x85, 0xa4, 0x70,
    };
    try testing.expectEqualSlices(u8, &expected1, &out1);

    // "hello" test
    var out2: [32]u8 = undefined;
    zig_keccak256("hello", 5, &out2);
    const input = "hello";
    zig_keccak256(input, input.len, &out2);
}

test "zig_keccak256_digest_size" {
    const testing = std.testing;
    try testing.expectEqual(@as(c_uint, 32), zig_keccak256_digest_size());
}

test "zig_ecdsa_verify rejects invalid" {
    const testing = std.testing;
    var hash: [32]u8 = [_]u8{0} ** 32;
    var sig_r: [32]u8 = [_]u8{0} ** 32;
    var sig_s: [32]u8 = [_]u8{0} ** 32;
    var pub_x: [32]u8 = [_]u8{0} ** 32;
    var pub_y: [32]u8 = [_]u8{0} ** 32;
    // Point at infinity should fail verification
    try testing.expectEqual(@as(c_int, 0), zig_ecdsa_verify(&hash, &sig_r, &sig_s, &pub_x, &pub_y));
}
