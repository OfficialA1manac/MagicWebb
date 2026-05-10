const std = @import("std");

/// Placeholder for batched secp256k1 EIP-712 verify.
/// Real implementation must link libsecp256k1 (or a Zig-native impl).
/// Returns false to force Go-side fallback until wired.
pub fn batchVerify(_: []const u8) bool {
    return false;
}
