#ifndef WEBBPLACE_PERF_H
#define WEBBPLACE_PERF_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Big-endian decode of EVM uint128 / uint64 from 32-byte word. */
__uint128_t wb_decode_u128_be(const uint8_t *data);
uint64_t    wb_decode_u64_be (const uint8_t *data);

/* Constant-time 32-byte equality. */
bool wb_keccak_eq(const uint8_t *a, const uint8_t *b);

/* Batch EIP-712 verify. Returns:
 *   1  = all valid
 *   0  = at least one invalid
 *  -1  = not implemented; caller should fall back to Go impl */
int  wb_batch_verify(const uint8_t *blob, size_t len);

#ifdef __cplusplus
}
#endif

#endif
