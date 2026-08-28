#include "conditioner.h"

#include <errno.h>
#include <math.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include <sodium.h>

#include "internal.h"

#define CH_CONDITIONER_MIN_BURST (64U * 1024U)

typedef struct ch_conditioner_stream {
    int local;
    int remote;
    ch_conditioner_config config;
} ch_conditioner_stream;

typedef struct ch_conditioner_direction {
    int source;
    int destination;
    const ch_conditioner_config *config;
    uint64_t bytes_per_second;
    ch_conditioner_bucket bucket;
} ch_conditioner_direction;

static uint64_t ch_conditioner_now(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value) != 0) return 0U;
    return (uint64_t)value.tv_sec * UINT64_C(1000000000) +
        (uint64_t)value.tv_nsec;
}

static void ch_conditioner_sleep(uint64_t nanoseconds) {
    if (nanoseconds == 0U) return;
    struct timespec requested = {
        .tv_sec = (time_t)(nanoseconds / UINT64_C(1000000000)),
        .tv_nsec = (long)(nanoseconds % UINT64_C(1000000000))
    };
    while (nanosleep(&requested, &requested) != 0 && errno == EINTR) {
    }
}

static uint64_t ch_conditioner_jitter(uint64_t maximum) {
    if (maximum == 0U) return 0U;
    uint64_t random = ((uint64_t)randombytes_random() << 32U) |
        randombytes_random();
    return random % maximum;
}

static void ch_conditioner_delay(const ch_conditioner_config *config) {
    if (config == NULL || !config->enabled) return;
    uint64_t jitter = ch_conditioner_jitter(config->jitter_nanoseconds);
    uint64_t total = config->latency_nanoseconds;
    if (UINT64_MAX - total < jitter) total = UINT64_MAX;
    else total += jitter;
    ch_conditioner_sleep(total);
}

static void ch_conditioner_wait(ch_conditioner_bucket *bucket,
                                uint64_t bytes_per_second,
                                size_t length) {
    if (bucket == NULL || bytes_per_second == 0U || length == 0U) return;
    double capacity = (double)bytes_per_second;
    if (capacity < (double)CH_CONDITIONER_MIN_BURST) {
        capacity = (double)CH_CONDITIONER_MIN_BURST;
    }
    size_t remaining = length;
    while (remaining > 0U) {
        size_t chunk = remaining;
        if ((double)chunk > capacity) chunk = (size_t)capacity;
        uint64_t now = ch_conditioner_now();
        if (bucket->last_nanoseconds == 0U) {
            bucket->last_nanoseconds = now;
            bucket->tokens = capacity;
        } else if (now > bucket->last_nanoseconds) {
            double elapsed = (double)(now - bucket->last_nanoseconds) /
                1000000000.0;
            bucket->tokens += elapsed * (double)bytes_per_second;
            if (bucket->tokens > capacity) bucket->tokens = capacity;
            bucket->last_nanoseconds = now;
        }
        if (bucket->tokens < (double)chunk) {
            double missing = (double)chunk - bucket->tokens;
            double seconds = missing / (double)bytes_per_second;
            uint64_t wait = (uint64_t)ceil(seconds * 1000000000.0);
            ch_conditioner_sleep(wait);
            continue;
        }
        bucket->tokens -= (double)chunk;
        remaining -= chunk;
    }
}

static uint64_t ch_conditioner_rate(const ch_config_table *conditioner,
                                    const char *key) {
    int64_t kbps = 0;
    ch_error ignored;
    if (conditioner == NULL || ch_config_table_get_int(
            conditioner, key, &kbps, &ignored) != CH_OK || kbps <= 0) {
        return 0U;
    }
    if ((uint64_t)kbps > UINT64_MAX / UINT64_C(125)) return UINT64_MAX;
    return (uint64_t)kbps * UINT64_C(125);
}

static uint64_t ch_conditioner_duration(const ch_config_table *conditioner,
                                        const char *key) {
    char *text = NULL;
    ch_error ignored;
    int64_t value = 0;
    if (conditioner == NULL || ch_config_table_get_string(
            conditioner, key, &text, &ignored) != CH_OK || text == NULL ||
        ch_config_parse_duration_ns(text, &value, &ignored) != CH_OK ||
        value <= 0) {
        free(text);
        return 0U;
    }
    free(text);
    return (uint64_t)value;
}

void ch_conditioner_config_load(const ch_config_table *profile,
                                ch_conditioner_config *out_config) {
    if (out_config == NULL) return;
    memset(out_config, 0, sizeof(*out_config));
    const ch_config_table *conditioner = ch_config_table_get_table(
        profile, "conditioner");
    bool enabled = false;
    ch_error ignored;
    (void)ch_config_table_get_bool(conditioner, "enabled", &enabled,
                                   &ignored);
    out_config->enabled = enabled ? 1 : 0;
    out_config->download_bytes_per_second = ch_conditioner_rate(
        conditioner, "download_kbps");
    out_config->upload_bytes_per_second = ch_conditioner_rate(
        conditioner, "upload_kbps");
    out_config->latency_nanoseconds = ch_conditioner_duration(
        conditioner, "latency");
    out_config->jitter_nanoseconds = ch_conditioner_duration(
        conditioner, "jitter");
    double loss = 0.0;
    if (ch_config_table_get_double(conditioner, "loss_percent", &loss,
                                   &ignored) != CH_OK) {
        int64_t integer = 0;
        if (ch_config_table_get_int(conditioner, "loss_percent", &integer,
                                    &ignored) == CH_OK) {
            loss = (double)integer;
        }
    }
    if (loss < 0.0) loss = 0.0;
    if (loss > 100.0) loss = 100.0;
    out_config->loss_probability = loss / 100.0;
}

int ch_conditioner_active(const ch_conditioner_config *config) {
    return config != NULL && config->enabled &&
        (config->download_bytes_per_second > 0U ||
         config->upload_bytes_per_second > 0U ||
         config->latency_nanoseconds > 0U ||
         config->jitter_nanoseconds > 0U ||
         config->loss_probability > 0.0);
}

int ch_conditioner_before_upload(const ch_conditioner_config *config,
                                 ch_conditioner_bucket *bucket,
                                 size_t length) {
    if (!ch_conditioner_active(config)) return 0;
    if (config->loss_probability >= 1.0) {
        ch_conditioner_delay(config);
        return 1;
    }
    if (config->loss_probability > 0.0) {
        double sample = (double)randombytes_random() /
            ((double)UINT32_MAX + 1.0);
        if (sample < config->loss_probability) {
            ch_conditioner_delay(config);
            return 1;
        }
    }
    ch_conditioner_wait(bucket, config->upload_bytes_per_second, length);
    ch_conditioner_delay(config);
    return 0;
}

void ch_conditioner_after_download(const ch_conditioner_config *config,
                                   ch_conditioner_bucket *bucket,
                                   size_t length) {
    if (!ch_conditioner_active(config)) return;
    ch_conditioner_wait(bucket, config->download_bytes_per_second, length);
    ch_conditioner_delay(config);
}

static int ch_conditioner_send_all(int descriptor, const uint8_t *bytes,
                                   size_t length) {
    while (length > 0U) {
        ssize_t written;
        do {
#ifdef MSG_NOSIGNAL
            written = send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
            written = send(descriptor, bytes, length, 0);
#endif
        } while (written < 0 && errno == EINTR);
        if (written <= 0) return 0;
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static void *ch_conditioner_direction_main(void *opaque) {
    ch_conditioner_direction *direction = opaque;
    uint8_t bytes[32768];
    for (;;) {
        ssize_t received;
        do {
            received = recv(direction->source, bytes, sizeof(bytes), 0);
        } while (received < 0 && errno == EINTR);
        if (received <= 0) break;
        ch_conditioner_wait(&direction->bucket,
                            direction->bytes_per_second,
                            (size_t)received);
        ch_conditioner_delay(direction->config);
        if (!ch_conditioner_send_all(direction->destination, bytes,
                                     (size_t)received)) break;
    }
    (void)shutdown(direction->destination, SHUT_WR);
    return NULL;
}

static void *ch_conditioner_stream_main(void *opaque) {
    ch_conditioner_stream *stream = opaque;
    ch_conditioner_direction upload = {
        .source = stream->local,
        .destination = stream->remote,
        .config = &stream->config,
        .bytes_per_second = stream->config.upload_bytes_per_second
    };
    ch_conditioner_direction download = {
        .source = stream->remote,
        .destination = stream->local,
        .config = &stream->config,
        .bytes_per_second = stream->config.download_bytes_per_second
    };
    pthread_t download_thread;
    int started = pthread_create(&download_thread, NULL,
                                 ch_conditioner_direction_main,
                                 &download) == 0;
    (void)ch_conditioner_direction_main(&upload);
    if (started) {
        (void)pthread_join(download_thread, NULL);
    } else {
        (void)shutdown(stream->remote, SHUT_RDWR);
    }
    (void)shutdown(stream->local, SHUT_RDWR);
    (void)close(stream->local);
    (void)shutdown(stream->remote, SHUT_RDWR);
    (void)close(stream->remote);
    free(stream);
    return NULL;
}

ch_status ch_conditioner_wrap_stream(
    int descriptor, const ch_conditioner_config *config,
    int *out_descriptor, ch_error *error) {
    ch_error_clear(error);
    if (descriptor < 0 || out_descriptor == NULL || config == NULL) {
        if (descriptor >= 0) {
            (void)shutdown(descriptor, SHUT_RDWR);
            (void)close(descriptor);
        }
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "conditioner stream input is invalid");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    if (!ch_conditioner_active(config)) {
        *out_descriptor = descriptor;
        return CH_OK;
    }
    int bridge[2] = {-1, -1};
    ch_conditioner_stream *stream = calloc(1U, sizeof(*stream));
    if (stream == NULL) {
        (void)shutdown(descriptor, SHUT_RDWR);
        (void)close(descriptor);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate conditioned stream bridge");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, bridge) != 0) {
        free(stream);
        if (bridge[0] >= 0) (void)close(bridge[0]);
        if (bridge[1] >= 0) (void)close(bridge[1]);
        (void)shutdown(descriptor, SHUT_RDWR);
        (void)close(descriptor);
        ch_error_set(error, CH_ERROR_IO,
                     "create conditioned stream bridge");
        return CH_ERROR_IO;
    }
    stream->local = bridge[1];
    stream->remote = descriptor;
    stream->config = *config;
    pthread_t worker;
    if (pthread_create(&worker, NULL, ch_conditioner_stream_main, stream) !=
            0) {
        (void)close(bridge[0]);
        (void)close(bridge[1]);
        (void)shutdown(descriptor, SHUT_RDWR);
        (void)close(descriptor);
        free(stream);
        ch_error_set(error, CH_ERROR_IO,
                     "start conditioned stream bridge");
        return CH_ERROR_IO;
    }
    (void)pthread_detach(worker);
    *out_descriptor = bridge[0];
    return CH_OK;
}
