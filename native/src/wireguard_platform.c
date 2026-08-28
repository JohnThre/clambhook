#include <openssl/rand.h>

#include <pthread.h>
#include <limits.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "crypto.h"
#include "wireguard-platform.h"

/* TAI64 uses 2^62 plus the original ten-second TAI offset. */
#define CH_WIREGUARD_TAI64_BASE UINT64_C(0x400000000000000a)

static pthread_mutex_t ch_wireguard_clock_mutex = PTHREAD_MUTEX_INITIALIZER;
static uint8_t ch_wireguard_last_timestamp[12];

uint32_t wireguard_sys_now(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value) != 0) abort();
    uint64_t milliseconds = (uint64_t)value.tv_sec * UINT64_C(1000) +
        (uint64_t)value.tv_nsec / UINT64_C(1000000);
    return (uint32_t)milliseconds;
}

void wireguard_random_bytes(void *bytes, size_t size) {
    uint8_t *cursor = bytes;
    while (size > 0U) {
        size_t amount = size > (size_t)INT_MAX ? (size_t)INT_MAX : size;
        if (RAND_bytes(cursor, (int)amount) != 1) abort();
        cursor += amount;
        size -= amount;
    }
}

static void ch_wireguard_increment_timestamp(uint8_t timestamp[12]) {
    for (size_t index = 12U; index > 0U; --index) {
        ++timestamp[index - 1U];
        if (timestamp[index - 1U] != 0U) break;
    }
}

void wireguard_tai64n_now(uint8_t *output) {
    struct timespec value;
    if (output == NULL || clock_gettime(CLOCK_REALTIME, &value) != 0) abort();
    uint64_t seconds = CH_WIREGUARD_TAI64_BASE + (uint64_t)value.tv_sec;
    U64TO8_BIG(output, seconds);
    U32TO8_BIG(output + 8U, (uint32_t)value.tv_nsec);

    (void)pthread_mutex_lock(&ch_wireguard_clock_mutex);
    if (memcmp(output, ch_wireguard_last_timestamp, 12U) <= 0) {
        memcpy(output, ch_wireguard_last_timestamp, 12U);
        ch_wireguard_increment_timestamp(output);
    }
    memcpy(ch_wireguard_last_timestamp, output, 12U);
    (void)pthread_mutex_unlock(&ch_wireguard_clock_mutex);
}

bool wireguard_is_under_load(void) {
    /* ClambHook is a client. It validates peer cookies but does not expose a
     * public responder that needs the optional DoS cookie challenge path. */
    return false;
}
