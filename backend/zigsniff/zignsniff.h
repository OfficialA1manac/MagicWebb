#ifndef ZIGSNIFF_H
#define ZIGSNIFF_H

#include <stddef.h>
#include <stdint.h>

/// Image format enum.
/// 0 = unknown, 1 = png, 2 = jpeg, 3 = gif, 4 = webp, 5 = avif, 6 = svg
#define ZIG_IMG_UNKNOWN 0
#define ZIG_IMG_PNG     1
#define ZIG_IMG_JPEG    2
#define ZIG_IMG_GIF     3
#define ZIG_IMG_WEBP    4
#define ZIG_IMG_AVIF    5
#define ZIG_IMG_SVG     6

/// Detects the image format from raw bytes using magic byte signatures.
/// Returns one of ZIG_IMG_* constants.
unsigned int zig_sniff_image(const uint8_t* data, size_t len);

/// Returns the MIME type string for a given image format constant.
/// Writes null-terminated string to `out` (must be at least 16 bytes).
void zig_image_mime(unsigned int format, uint8_t* out);

#endif // ZIGSNIFF_H
