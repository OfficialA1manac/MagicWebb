const std = @import("std");

/// Big-endian decode of a 16-byte uint128 from a 32-byte EVM word
/// (data points at start of the 32-byte word; high 16 bytes are zero-padding).
pub fn decodeU128BE(data: [*]const u8) u128 {
    var v: u128 = 0;
    var i: usize = 16;
    while (i < 32) : (i += 1) v = (v << 8) | @as(u128, data[i]);
    return v;
}

pub fn decodeU64BE(data: [*]const u8) u64 {
    var v: u64 = 0;
    var i: usize = 24;
    while (i < 32) : (i += 1) v = (v << 8) | @as(u64, data[i]);
    return v;
}

test "decodeU128BE" {
    var buf = [_]u8{0} ** 32;
    buf[31] = 0xFF;
    try std.testing.expectEqual(@as(u128, 0xFF), decodeU128BE(&buf));
}
