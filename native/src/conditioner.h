#ifndef CLAMBHOOK_CONDITIONER_H
#define CLAMBHOOK_CONDITIONER_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

typedef struct ch_conditioner_config {
    int enabled;
    uint64_t download_bytes_per_second;
    uint64_t upload_bytes_per_second;
    uint64_t latency_nanoseconds;
    uint64_t jitter_nanoseconds;
    double loss_probability;
} ch_conditioner_config;

typedef struct ch_conditioner_bucket {
    double tokens;
    uint64_t last_nanoseconds;
} ch_conditioner_bucket;

void ch_conditioner_config_load(const ch_config_table *profile,
                                ch_conditioner_config *out_config);
int ch_conditioner_active(const ch_conditioner_config *config);

/* Takes ownership of descriptor on every path. */
ch_status ch_conditioner_wrap_stream(
    int descriptor, const ch_conditioner_config *config,
    int *out_descriptor, ch_error *error);

/* Returns non-zero when an outbound datagram should be silently dropped. */
int ch_conditioner_before_upload(const ch_conditioner_config *config,
                                 ch_conditioner_bucket *bucket,
                                 size_t length);
void ch_conditioner_after_download(const ch_conditioner_config *config,
                                   ch_conditioner_bucket *bucket,
                                   size_t length);

#endif
