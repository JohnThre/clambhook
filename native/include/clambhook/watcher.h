// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_WATCHER_H
#define CLAMBHOOK_WATCHER_H

#include <stdint.h>

#include <uv.h>

#include "clambhook/config.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_config_watcher ch_config_watcher;

/* The parsed config is borrowed for the duration of the callback. */
typedef ch_status (*ch_config_reload_callback)(const ch_config *config,
                                               void *context,
                                               ch_error *error);
typedef void (*ch_config_reload_event_callback)(bool succeeded,
                                                const ch_error *error,
                                                void *context);

typedef struct ch_config_watcher_options {
    uint64_t debounce_milliseconds;
    ch_config_reload_callback reload;
    ch_config_reload_event_callback event;
    void *context;
} ch_config_watcher_options;

/* Polls the resolved path so writes and atomic file replacement are observed. */
ch_config_watcher *ch_config_watcher_start(uv_loop_t *loop, const char *path,
                                           const ch_config_watcher_options *options,
                                           ch_error *error);

/* Idempotent. The watcher releases itself after libuv closes both handles. */
void ch_config_watcher_stop(ch_config_watcher *watcher);

#ifdef __cplusplus
}
#endif

#endif
