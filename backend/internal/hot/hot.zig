// Zig hot-path for WebbPlace indexer.
// Build target: shared library `libwebbhot` consumed via CGo.
// Build: `zig build-lib -O ReleaseFast -dynamic hot.zig -femit-bin=lib/libwebbhot.so`
//
// Three exported functions:
//   wp_decode_log   — match topic0 selector, ABI-decode event data → JSON
//   wp_keccak256    — keccak-256 hash
//   wp_compute_score — trending score with exponential time decay

const std = @import("std");
const math = std.math;

// ── Event selectors (keccak256 of event signature) ────────────────────────
// Precomputed at comptime to avoid runtime hashing overhead.
// Generate with: cast keccak "Listed(address,uint256,address,uint256,uint64,uint8,uint64)"
// (placeholders — replace with actual values after ABI is finalised)
const SEL_LISTED:            [32]u8 = .{0x00} ** 32; // TODO: fill after ABI lock
const SEL_DELISTED:          [32]u8 = .{0x00} ** 32;
const SEL_SALE:              [32]u8 = .{0x00} ** 32;
const SEL_AUCTION_CREATED:   [32]u8 = .{0x00} ** 32;
const SEL_BID_PLACED:        [32]u8 = .{0x00} ** 32;
const SEL_AUCTION_SETTLED:   [32]u8 = .{0x00} ** 32;
const SEL_AUCTION_CANCELLED: [32]u8 = .{0x00} ** 32;
const SEL_OFFER_ACCEPTED:    [32]u8 = .{0x00} ** 32;

const WpEventType = enum(c_int) {
    unknown           = 0,
    listed            = 1,
    delisted          = 2,
    sale              = 3,
    auction_created   = 4,
    bid_placed        = 5,
    auction_settled   = 6,
    auction_cancelled = 7,
    offer_accepted    = 8,
};

const WpScoreWeights = extern struct {
    w_views:      f64,
    w_bids:       f64,
    w_volume:     f64,
    decay_lambda: f64,
};

// ── wp_keccak256 ───────────────────────────────────────────────────────────
export fn wp_keccak256(in_ptr: [*c]const u8, in_len: usize, out: [*c]u8) void {
    var hasher = std.crypto.hash.sha3.Keccak256.init(.{});
    hasher.update(in_ptr[0..in_len]);
    var digest: [32]u8 = undefined;
    hasher.final(&digest);
    @memcpy(out[0..32], &digest);
}

// ── wp_decode_log ──────────────────────────────────────────────────────────
export fn wp_decode_log(
    topic0:         [*c]const u8,
    data:           [*c]const u8,
    data_len:       usize,
    out:            [*c]u8,
    out_cap:        usize,
    event_type_out: *WpEventType,
) usize {
    const t: [32]u8 = topic0[0..32].*;

    // Match selector
    const ev_type: WpEventType = blk: {
        if (std.mem.eql(u8, &t, &SEL_LISTED))            break :blk .listed;
        if (std.mem.eql(u8, &t, &SEL_DELISTED))          break :blk .delisted;
        if (std.mem.eql(u8, &t, &SEL_SALE))              break :blk .sale;
        if (std.mem.eql(u8, &t, &SEL_AUCTION_CREATED))   break :blk .auction_created;
        if (std.mem.eql(u8, &t, &SEL_BID_PLACED))        break :blk .bid_placed;
        if (std.mem.eql(u8, &t, &SEL_AUCTION_SETTLED))   break :blk .auction_settled;
        if (std.mem.eql(u8, &t, &SEL_AUCTION_CANCELLED)) break :blk .auction_cancelled;
        if (std.mem.eql(u8, &t, &SEL_OFFER_ACCEPTED))    break :blk .offer_accepted;
        break :blk .unknown;
    };

    event_type_out.* = ev_type;
    if (ev_type == .unknown) return 0;

    // Stub: ABI decode → JSON.
    // Production: implement full ABI decoder for each event's param layout.
    // For now, emit raw hex so Go side can decode with go-ethereum/abi.
    var buf: [8192]u8 = undefined;
    var fbs = std.io.fixedBufferStream(&buf);
    const writer = fbs.writer();

    writer.print("{{\"type\":{d},\"raw\":\"0x", .{@intFromEnum(ev_type)}) catch return 0;
    for (data[0..data_len]) |byte| {
        writer.print("{x:0>2}", .{byte}) catch return 0;
    }
    writer.writeAll("\"}") catch return 0;

    const written = fbs.pos;
    if (written > out_cap) return 0;
    @memcpy(out[0..written], buf[0..written]);
    return written;
}

// ── wp_compute_score ───────────────────────────────────────────────────────
export fn wp_compute_score(
    views:      u64,
    bids:       u64,
    volume_eth: f64,
    age_hours:  f64,
    weights:    WpScoreWeights,
) f64 {
    const raw: f64 =
        @as(f64, @floatFromInt(views)) * weights.w_views +
        @as(f64, @floatFromInt(bids))  * weights.w_bids +
        volume_eth                     * weights.w_volume;

    return raw * math.exp(-weights.decay_lambda * age_hours);
}
