// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/watcher.h"

#include <errno.h>
#include <limits.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "internal.h"

struct ch_config_watcher {
    uv_fs_poll_t poll_handle;
    uv_timer_t debounce_timer;
    char *path;
    char *directory;
    char *basename;
    ch_config_watcher_options options;
    unsigned int close_count;
    bool stopping;
};

static void ch_config_watcher_handle_closed(uv_handle_t *handle) {
    ch_config_watcher *watcher = handle->data;
    ++watcher->close_count;
    if (watcher->close_count == 2U) {
        free(watcher->path);
        free(watcher->directory);
        free(watcher->basename);
        free(watcher);
    }
}

static void ch_config_watcher_fire(uv_timer_t *timer) {
    ch_config_watcher *watcher = timer->data;
    ch_config *config = NULL;
    ch_error error;
    ch_status status;
    if (watcher->stopping) return;
    status = ch_config_load(watcher->path, &config, &error);
    if (status == CH_OK) {
        status = watcher->options.reload(config, watcher->options.context, &error);
    }
    if (watcher->options.event != NULL) {
        watcher->options.event(status == CH_OK, &error, watcher->options.context);
    }
    ch_config_free(config);
}

static void ch_config_watcher_poll(uv_fs_poll_t *poll_handle, int status,
                                   const uv_stat_t *previous,
                                   const uv_stat_t *current) {
    ch_config_watcher *watcher = poll_handle->data;
    (void)previous;
    (void)current;
    if (status < 0 || watcher->stopping) return;
    (void)uv_timer_stop(&watcher->debounce_timer);
    (void)uv_timer_start(&watcher->debounce_timer, ch_config_watcher_fire,
                         watcher->options.debounce_milliseconds, 0U);
}

static ch_status ch_config_watcher_paths(const char *path, char **out_path,
                                         char **out_directory, char **out_basename,
                                         ch_error *error) {
    char resolved[PATH_MAX];
    char *slash;
    if (realpath(path, resolved) == NULL) {
        ch_error_set(error, CH_ERROR_IO, "resolve config watcher path: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    *out_path = ch_strdup(resolved);
    if (*out_path == NULL) goto out_of_memory;
    slash = strrchr(resolved, '/');
    if (slash == NULL) {
        *out_directory = ch_strdup(".");
        *out_basename = ch_strdup(resolved);
    } else {
        *slash = '\0';
        *out_directory = ch_strdup(resolved[0] == '\0' ? "/" : resolved);
        *out_basename = ch_strdup(slash + 1);
    }
    if (*out_directory == NULL || *out_basename == NULL) goto out_of_memory;
    return CH_OK;

out_of_memory:
    free(*out_path); free(*out_directory); free(*out_basename);
    *out_path = NULL; *out_directory = NULL; *out_basename = NULL;
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate config watcher paths");
    return CH_ERROR_OUT_OF_MEMORY;
}

ch_config_watcher *ch_config_watcher_start(uv_loop_t *loop, const char *path,
                                           const ch_config_watcher_options *options,
                                           ch_error *error) {
    ch_config_watcher *watcher;
    int result;
    ch_error_clear(error);
    if (loop == NULL || path == NULL || path[0] == '\0' || options == NULL ||
        options->reload == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "watcher loop, path, options, and reload callback are required");
        return NULL;
    }
    watcher = calloc(1U, sizeof(*watcher));
    if (watcher == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate config watcher");
        return NULL;
    }
    watcher->options = *options;
    if (watcher->options.debounce_milliseconds == 0U) {
        watcher->options.debounce_milliseconds = 250U;
    }
    if (ch_config_watcher_paths(path, &watcher->path, &watcher->directory,
                                &watcher->basename, error) != CH_OK) {
        free(watcher);
        return NULL;
    }
    result = uv_fs_poll_init(loop, &watcher->poll_handle);
    if (result != 0) {
        free(watcher->path); free(watcher->directory); free(watcher->basename); free(watcher);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize config watcher: %s", uv_strerror(result));
        return NULL;
    }
    watcher->poll_handle.data = watcher;
    result = uv_timer_init(loop, &watcher->debounce_timer);
    if (result != 0) {
        watcher->close_count = 1U;
        uv_close((uv_handle_t *)&watcher->poll_handle, ch_config_watcher_handle_closed);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize config watcher timer: %s",
                     uv_strerror(result));
        return NULL;
    }
    watcher->debounce_timer.data = watcher;
    result = uv_fs_poll_start(&watcher->poll_handle, ch_config_watcher_poll,
                              watcher->path, 100U);
    if (result != 0) {
        uv_close((uv_handle_t *)&watcher->poll_handle, ch_config_watcher_handle_closed);
        uv_close((uv_handle_t *)&watcher->debounce_timer, ch_config_watcher_handle_closed);
        ch_error_set(error, CH_ERROR_IO, "watch config directory: %s", uv_strerror(result));
        return NULL;
    }
    return watcher;
}

void ch_config_watcher_stop(ch_config_watcher *watcher) {
    if (watcher == NULL || watcher->stopping) return;
    watcher->stopping = true;
    (void)uv_fs_poll_stop(&watcher->poll_handle);
    (void)uv_timer_stop(&watcher->debounce_timer);
    if (!uv_is_closing((uv_handle_t *)&watcher->poll_handle)) {
        uv_close((uv_handle_t *)&watcher->poll_handle, ch_config_watcher_handle_closed);
    }
    if (!uv_is_closing((uv_handle_t *)&watcher->debounce_timer)) {
        uv_close((uv_handle_t *)&watcher->debounce_timer, ch_config_watcher_handle_closed);
    }
}
