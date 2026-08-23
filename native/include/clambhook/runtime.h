#ifndef CLAMBHOOK_RUNTIME_H
#define CLAMBHOOK_RUNTIME_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_runtime ch_runtime;

typedef void (*ch_packet_writer)(const uint8_t *packet, size_t length, void *context);
typedef void (*ch_log_writer)(int level, const char *message, void *context);

typedef struct ch_runtime_options {
    ch_packet_writer packet_writer;
    void *packet_writer_context;
    ch_log_writer log_writer;
    void *log_writer_context;
} ch_runtime_options;

/*
 * Runtime operations are serialized on an internal libuv event-loop thread.
 * Returned strings are UTF-8 JSON allocated by the runtime and must be
 * released with ch_string_free(). Callback buffers are borrowed and remain
 * valid only for the duration of the callback.
 */
ch_runtime *ch_runtime_create(const ch_runtime_options *options, ch_error *error);
void ch_runtime_destroy(ch_runtime *runtime);

ch_status ch_runtime_start(ch_runtime *runtime, const char *config_path, ch_error *error);
ch_status ch_runtime_stop(ch_runtime *runtime, ch_error *error);
ch_status ch_runtime_reload(ch_runtime *runtime, const char *config_path, ch_error *error);
ch_status ch_runtime_inject_packet(
    ch_runtime *runtime,
    const uint8_t *packet,
    size_t length,
    ch_error *error
);

bool ch_runtime_is_running(ch_runtime *runtime);

/*
 * Query and mutation operation names are stable bridge identifiers. They
 * deliberately keep JNI thin while preserving the existing JSON contracts.
 */
ch_status ch_runtime_query(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
);
ch_status ch_runtime_mutate(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
);

void ch_string_free(char *string);

#ifdef __cplusplus
}
#endif

#endif
