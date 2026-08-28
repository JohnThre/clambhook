// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <uv.h>

#include "clambhook/watcher.h"

typedef struct watcher_test_state {
    uv_loop_t loop;
    uv_timer_t writer;
    uv_timer_t timeout;
    ch_config_watcher *watcher;
    char path[160];
    int reloaded;
    int correct_profile;
    int timed_out;
} watcher_test_state;

static ch_status watcher_test_reload(const ch_config *config, void *context,
                                     ch_error *error) {
    watcher_test_state *state = context;
    const ch_config_table *profile = ch_config_active_profile(config);
    char *name = NULL;
    state->reloaded = 1;
    if (ch_config_table_get_string(profile, "name", &name, error) == CH_OK) {
        state->correct_profile = strcmp(name, "two") == 0;
        free(name);
    }
    ch_config_watcher_stop(state->watcher);
    state->watcher = NULL;
    (void)uv_timer_stop(&state->timeout);
    uv_close((uv_handle_t *)&state->timeout, NULL);
    return state->correct_profile ? CH_OK : CH_ERROR_INVALID_STATE;
}

static void watcher_test_write(uv_timer_t *timer) {
    watcher_test_state *state = timer->data;
    FILE *file = fopen(state->path, "wb");
    if (file != NULL) {
        (void)fputs("active = \"two\"\n[[profile]]\nname = \"two\"\n", file);
        (void)fclose(file);
    }
    uv_close((uv_handle_t *)timer, NULL);
}

static void watcher_test_timeout(uv_timer_t *timer) {
    watcher_test_state *state = timer->data;
    state->timed_out = 1;
    ch_config_watcher_stop(state->watcher);
    state->watcher = NULL;
    uv_close((uv_handle_t *)timer, NULL);
}

void ch_test_watcher(void) {
    watcher_test_state state;
    ch_config_watcher_options options;
    ch_error error;
    FILE *file;
    memset(&state, 0, sizeof(state));
    (void)snprintf(state.path, sizeof(state.path),
                   "/tmp/clambhook-watcher-%ld.toml", (long)getpid());
    file = fopen(state.path, "wb");
    CH_TEST_ASSERT(file != NULL);
    CH_TEST_ASSERT(fputs("active = \"one\"\n[[profile]]\nname = \"one\"\n", file) >= 0);
    CH_TEST_ASSERT(fclose(file) == 0);
    CH_TEST_ASSERT(uv_loop_init(&state.loop) == 0);
    memset(&options, 0, sizeof(options));
    options.debounce_milliseconds = 20U;
    options.reload = watcher_test_reload;
    options.context = &state;
    state.watcher = ch_config_watcher_start(&state.loop, state.path, &options, &error);
    CH_TEST_ASSERT(state.watcher != NULL);
    CH_TEST_ASSERT(uv_timer_init(&state.loop, &state.writer) == 0);
    CH_TEST_ASSERT(uv_timer_init(&state.loop, &state.timeout) == 0);
    state.writer.data = &state;
    state.timeout.data = &state;
    CH_TEST_ASSERT(uv_timer_start(&state.writer, watcher_test_write, 300U, 0U) == 0);
    CH_TEST_ASSERT(uv_timer_start(&state.timeout, watcher_test_timeout, 5000U, 0U) == 0);
    (void)uv_run(&state.loop, UV_RUN_DEFAULT);
    CH_TEST_ASSERT(!state.timed_out);
    CH_TEST_ASSERT(state.reloaded);
    CH_TEST_ASSERT(state.correct_profile);
    CH_TEST_ASSERT(uv_loop_close(&state.loop) == 0);
    CH_TEST_ASSERT(unlink(state.path) == 0);
}
