const std = @import("std");
pub const log_decode = @import("log_decode.zig");
pub const sig_verify = @import("sig_verify.zig");

// Re-exports for C ABI.
export fn wb_decode_u128_be(data: [*]const u8) callconv(.C) u128 {
    return log_decode.decodeU128BE(data);
}
export fn wb_decode_u64_be(data: [*]const u8) callconv(.C) u64 {
    return log_decode.decodeU64BE(data);
}
export fn wb_keccak_eq(a: [*]const u8, b: [*]const u8) callconv(.C) bool {
    var i: usize = 0;
    while (i < 32) : (i += 1) if (a[i] != b[i]) return false;
    return true;
}

/// Stub: returns 1 if all sigs valid, 0 otherwise. Real impl needs secp256k1.
/// Until linked against libsecp256k1, Go must use its fallback.
export fn wb_batch_verify(_: [*]const u8, _: usize) callconv(.C) c_int {
    return -1; // not implemented; signal Go to fall back
}
