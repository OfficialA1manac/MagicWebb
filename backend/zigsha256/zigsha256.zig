//! Zig SHA-256 implementation using the standard library.
//! Compile to a static library for linking via CGO:
//!   zig build-lib -O ReleaseFast -dynamic zigsha256.zig
//!
//! Exports a single C-ABI function `zig_sha256` that computes
//! a SHA-256 hash of `data` (len bytes) and writes the 32-byte
//! result to `out`. The caller owns the output buffer (32 bytes).

const std = @import("std");
const crypto = std.crypto;

/// Computes SHA-256(data) and writes the 32-byte digest to `out`.
/// `out` must point to a writable buffer of at least 32 bytes.
export fn zig_sha256(data: [*]const u8, len: usize, out: [*]u8) callconv(.C) void {
    var hash: [32]u8 = undefined;
    crypto.hash.sha2.Sha256.hash(data[0..len], &hash, .{});
    @memcpy(out[0..32], hash[0..]);
}

/// Returns the SHA-256 digest size in bytes (always 32).
/// Useful for callers that need to allocate the output buffer.
export fn zig_sha256_digest_size() callconv(.C) c_uint {
    return 32;
}

test "zig_sha256 produces correct digest" {
    const testing = std.testing;

    // Empty string: SHA-256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
    var out1: [32]u8 = undefined;
    zig_sha256("", 0, &out1);
    const expected1 = [_]u8{
        0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14,
        0x9a, 0xfb, 0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24,
        0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b, 0x93, 0x4c,
        0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
    };
    try testing.expectEqualSlices(u8, &expected1, &out1);

    // "abc": SHA-256("abc") = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
    var out2: [32]u8 = undefined;
    zig_sha256("abc", 3, &out2);
    const expected2 = [_]u8{
        0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea,
        0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22, 0x23,
        0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c,
        0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad,
    };
    try testing.expectEqualSlices(u8, &expected2, &out2);

    // "hello world" as a larger input
    var out3: [32]u8 = undefined;
    const input = "hello world";
    zig_sha256(input, input.len, &out3);
    const expected3 = [_]u8{
        0xb9, 0x4d, 0x27, 0xb9, 0x93, 0x4d, 0x3e, 0x08,
        0xa5, 0x2e, 0x52, 0xd7, 0xda, 0x7d, 0xab, 0xfa,
        0xc4, 0x84, 0xef, 0xe3, 0x7a, 0x53, 0x80, 0xee,
        0x90, 0x88, 0xf7, 0xac, 0xe2, 0xef, 0xcd, 0xe9,
    };
    try testing.expectEqualSlices(u8, &expected3, &out3);
}

test "zig_sha256_digest_size" {
    const testing = std.testing;
    try testing.expectEqual(@as(c_uint, 32), zig_sha256_digest_size());
}
