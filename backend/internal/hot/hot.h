#pragma once
#include <stdint.h>
#include <stddef.h>

// ── Log decode ─────────────────────────────────────────────────────────────
// Matches topic[0] against WebbPlace event selectors.
// Returns 0 if unrecognised.
// event_type values mirror the proto EventType enum.
typedef enum {
    WP_EVENT_UNKNOWN          = 0,
    WP_EVENT_LISTED           = 1,
    WP_EVENT_DELISTED         = 2,
    WP_EVENT_SALE             = 3,
    WP_EVENT_AUCTION_CREATED  = 4,
    WP_EVENT_BID_PLACED       = 5,
    WP_EVENT_AUCTION_SETTLED  = 6,
    WP_EVENT_AUCTION_CANCELLED= 7,
    WP_EVENT_OFFER_ACCEPTED   = 8,
} wp_event_type_t;

typedef struct {
    const uint8_t *data;
    size_t         len;
} wp_slice_t;

// Decode ABI-encoded log data after topic matching.
// topic0: 32-byte keccak selector
// data / data_len: ABI-encoded non-indexed params
// out / out_cap: output buffer for decoded JSON
// Returns bytes written, or 0 on error.
extern size_t wp_decode_log(
    const uint8_t *topic0,
    const uint8_t *data, size_t data_len,
    char *out, size_t out_cap,
    wp_event_type_t *event_type_out
);

// ── Keccak-256 ─────────────────────────────────────────────────────────────
// Writes 32-byte hash into out. out must be at least 32 bytes.
extern void wp_keccak256(const uint8_t *in, size_t in_len, uint8_t *out);

// ── Trending score ─────────────────────────────────────────────────────────
typedef struct {
    double w_views;   // weight for view count
    double w_bids;    // weight for bid count
    double w_volume;  // weight for volume (ETH, float)
    double decay_lambda; // exponential decay rate (per hour)
} wp_score_weights_t;

// Computes: (views*w_views + bids*w_bids + volume_eth*w_volume) * exp(-lambda*age_hours)
extern double wp_compute_score(
    uint64_t views,
    uint64_t bids,
    double   volume_eth,
    double   age_hours,
    wp_score_weights_t weights
);
