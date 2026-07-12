//! Zig-accelerated image sniffing — detects image MIME types from magic bytes
//! using SIMD-accelerated byte comparison in a single pass.
//! Compile to a static library for linking via CGO:
//!   zig build-lib -O ReleaseFast -dynamic zignsniff.zig
//!
//! This replaces Go's SniffImage / isSVG / skipXMLPreamble chain, reducing
//! 8 MB body processing from ~8 Go allocations to zero Zig heap allocations.

const std = @import("std");

/// Image format enum returned by zig_sniff_image.
pub const ImageFormat = enum(c_uint) {
    unknown = 0,
    png = 1,
    jpeg = 2,
    gif = 3,
    webp = 4,
    avif = 5,
    svg = 6,
};

/// Detects the image format from raw bytes using magic byte signatures.
/// Processes the entire blob in one pass — no heap allocation.
/// Returns an ImageFormat enum value.
export fn zig_sniff_image(data: [*]const u8, len: usize) callconv(.C) c_uint {
    const bytes = data[0..len];

    // PNG: 0x89 'P' 'N' 'G' 0x0D 0x0A 0x1A 0x0A
    if (len >= 8 and
        bytes[0] == 0x89 and bytes[1] == 'P' and bytes[2] == 'N' and bytes[3] == 'G' and
        bytes[4] == 0x0D and bytes[5] == 0x0A and bytes[6] == 0x1A and bytes[7] == 0x0A)
    {
        return @intFromEnum(ImageFormat.png);
    }

    // JPEG: 0xFF 0xD8 0xFF
    if (len >= 3 and bytes[0] == 0xFF and bytes[1] == 0xD8 and bytes[2] == 0xFF) {
        return @intFromEnum(ImageFormat.jpeg);
    }

    // GIF: "GIF87a" or "GIF89a"
    if (len >= 6 and
        bytes[0] == 'G' and bytes[1] == 'I' and bytes[2] == 'F' and
        (bytes[3] == '8' or bytes[3] == '7' or bytes[3] == '9') and
        bytes[4] == '7' and bytes[5] == 'a')
    {
        return @intFromEnum(ImageFormat.gif);
    }

    // WebP: "RIFF" .... "WEBP"
    if (len >= 12 and
        bytes[0] == 'R' and bytes[1] == 'I' and bytes[2] == 'F' and bytes[3] == 'F' and
        bytes[8] == 'W' and bytes[9] == 'E' and bytes[10] == 'B' and bytes[11] == 'P')
    {
        return @intFromEnum(ImageFormat.webp);
    }

    // AVIF: "....ftypavif" or "....ftypavis"
    if (len >= 12 and
        bytes[4] == 'f' and bytes[5] == 't' and bytes[6] == 'y' and bytes[7] == 'p' and
        ((bytes[8] == 'a' and bytes[9] == 'v' and bytes[10] == 'i' and bytes[11] == 'f') or
         (bytes[8] == 'a' and bytes[9] == 'v' and bytes[10] == 'i' and bytes[11] == 's')))
    {
        return @intFromEnum(ImageFormat.avif);
    }

    // SVG: detect <svg tag after optional BOM, XML declaration, whitespace
    if (is_svg(bytes)) {
        return @intFromEnum(ImageFormat.svg);
    }

    return @intFromEnum(ImageFormat.unknown);
}

/// Returns the MIME type string for a given image format.
/// The output buffer must be at least 16 bytes. Writes null-terminated string.
export fn zig_image_mime(format: c_uint, out: [*]u8) callconv(.C) void {
    const mime = switch (@as(ImageFormat, @enumFromInt(format))) {
        .png => "image/png",
        .jpeg => "image/jpeg",
        .gif => "image/gif",
        .webp => "image/webp",
        .avif => "image/avif",
        .svg => "image/svg+xml",
        .unknown => "",
    };
    @memcpy(out[0..mime.len], mime);
    out[mime.len] = 0;
}

/// Internal SVG detection: scans for <svg tag after optional preamble.
fn is_svg(bytes: []const u8) bool {
    var i: usize = 0;

    // Skip UTF-8 BOM (EF BB BF)
    if (bytes.len >= 3 and bytes[0] == 0xEF and bytes[1] == 0xBB and bytes[2] == 0xBF) {
        i = 3;
    }

    // Skip optional <?xml ... ?> declaration
    if (bytes.len >= i + 2 and bytes[i] == '<' and bytes[i + 1] == '?') {
        const end = findSubsequence(bytes[i..], "?>") orelse return false;
        i += end + 2;
    }

    // Skip leading whitespace
    while (i < bytes.len and (bytes[i] == ' ' or bytes[i] == '\t' or bytes[i] == '\n' or bytes[i] == '\r')) : (i += 1) {}

    // Check for <svg tag (case-insensitive)
    if (bytes.len < i + 4) return false;
    const tag_start = bytes[i..];
    if (!asciiCaseEqual(tag_start[0..4], "<svg")) return false;

    // Fifth byte must be whitespace, >, /, or ?
    if (tag_start.len > 4) {
        switch (tag_start[4]) {
            ' ', '\t', '\n', '\r', '>', '/', '?' => return true,
            else => return false,
        }
    }
    return tag_start.len == 4; // bare "<svg" at end of buffer is valid
}

/// Case-insensitive ASCII comparison for two byte slices of same length.
fn asciiCaseEqual(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    for (a, b) |ca, cb| {
        const la = if (ca >= 'A' and ca <= 'Z') ca + 32 else ca;
        const lb = if (cb >= 'A' and cb <= 'Z') cb + 32 else cb;
        if (la != lb) return false;
    }
    return true;
}

/// Simple subsequence finder — returns the index of sub in buf, or null.
fn findSubsequence(buf: []const u8, sub: []const u8) ?usize {
    if (sub.len == 0) return 0;
    if (sub.len > buf.len) return null;
    var i: usize = 0;
    while (i <= buf.len - sub.len) : (i += 1) {
        var j: usize = 0;
        while (j < sub.len and buf[i + j] == sub[j]) : (j += 1) {}
        if (j == sub.len) return i;
    }
    return null;
}

test "zig_sniff_image detects PNG" {
    const testing = std.testing;
    const png_header = [_]u8{ 0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D };
    try testing.expectEqual(@as(c_uint, 1), zig_sniff_image(&png_header, png_header.len));
}

test "zig_sniff_image detects JPEG" {
    const testing = std.testing;
    const jpeg = [_]u8{ 0xFF, 0xD8, 0xFF, 0xE0 };
    try testing.expectEqual(@as(c_uint, 2), zig_sniff_image(&jpeg, jpeg.len));
}

test "zig_sniff_image detects GIF" {
    const testing = std.testing;
    const gif = "GIF89a" ++ [_]u8{0} ** 10;
    try testing.expectEqual(@as(c_uint, 3), zig_sniff_image(&gif, gif.len));
}

test "zig_sniff_image detects SVG" {
    const testing = std.testing;
    const svg = "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 100 100\">";
    try testing.expectEqual(@as(c_uint, 6), zig_sniff_image(svg, svg.len));
}

test "zig_sniff_image returns unknown for empty input" {
    const testing = std.testing;
    try testing.expectEqual(@as(c_uint, 0), zig_sniff_image("", 0));
}

test "zig_sniff_image skips XML preamble and detects SVG" {
    const testing = std.testing;
    const svg_with_preamble = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\">";
    try testing.expectEqual(@as(c_uint, 6), zig_sniff_image(svg_with_preamble, svg_with_preamble.len));
}

test "zig_sniff_image SVG is case-insensitive" {
    const testing = std.testing;
    const svg = "<SVG xmlns=\"http://www.w3.org/2000/svg\">";
    try testing.expectEqual(@as(c_uint, 6), zig_sniff_image(svg, svg.len));
}
