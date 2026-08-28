// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <uv.h>

#include "api_server.h"
#include "clambhook/config.h"
#include "clambhook/runtime.h"
#include "clambhook/watcher.h"

#ifndef CLAMBHOOK_VERSION
#define CLAMBHOOK_VERSION "dev"
#endif

typedef struct daemon_state {
    ch_runtime *runtime;
    ch_api_server *api_server;
    ch_config_watcher *config_watcher;
    uv_signal_t interrupt_signal;
    uv_signal_t terminate_signal;
} daemon_state;

static void usage(FILE *stream) {
    fprintf(
        stream,
        "usage: clambhook-c [-version] [--allow-incomplete-native] "
        "[-api host:port] [-api-token token] [-config path] [-license path] [-no-watch]\n"
    );
}

static const char *next_argument(int argc, char **argv, int *index, const char *name) {
    if (*index + 1 >= argc) {
        fprintf(stderr, "%s requires a value\n", name);
        return NULL;
    }
    return argv[++*index];
}

static void stop_daemon(uv_signal_t *signal_handle, int signal_number) {
    (void)signal_number;
    daemon_state *state = signal_handle->data;
    ch_error error;
    ch_config_watcher_stop(state->config_watcher);
    state->config_watcher = NULL;
    (void)ch_runtime_stop(state->runtime, &error);
    ch_api_server_stop(state->api_server);
    if (!uv_is_closing((uv_handle_t *)&state->interrupt_signal)) {
        (void)uv_signal_stop(&state->interrupt_signal);
        uv_close((uv_handle_t *)&state->interrupt_signal, NULL);
    }
    if (!uv_is_closing((uv_handle_t *)&state->terminate_signal)) {
        (void)uv_signal_stop(&state->terminate_signal);
        uv_close((uv_handle_t *)&state->terminate_signal, NULL);
    }
}

static ch_status reload_daemon_config(const ch_config *config, void *context,
                                      ch_error *error) {
    daemon_state *state = context;
    return ch_runtime_reload(state->runtime, ch_config_source_path(config), error);
}

static void report_reload(bool succeeded, const ch_error *error, void *context) {
    (void)context;
    if (succeeded) {
        fprintf(stderr, "configuration reloaded\n");
    } else {
        fprintf(stderr, "configuration reload rejected: %s\n",
                error == NULL ? "unknown error" : error->message);
    }
}

int main(int argc, char **argv) {
    const char *api_address = "127.0.0.1:9090";
    const char *api_token = getenv("CLAMBHOOK_API_TOKEN");
    const char *config_path = "";
    char *configured_api_address = NULL;
    int show_version = 0;
    int allow_incomplete = 0;
    int api_address_explicit = 0;
    int no_watch = 0;
    for (int index = 1; index < argc; ++index) {
        if (strcmp(argv[index], "-version") == 0 || strcmp(argv[index], "--version") == 0) {
            show_version = 1;
        } else if (strcmp(argv[index], "--allow-incomplete-native") == 0) {
            allow_incomplete = 1;
        } else if (strcmp(argv[index], "-api") == 0) {
            api_address = next_argument(argc, argv, &index, "-api");
            if (api_address == NULL) return 2;
            api_address_explicit = 1;
        } else if (strcmp(argv[index], "-api-token") == 0) {
            api_token = next_argument(argc, argv, &index, "-api-token");
            if (api_token == NULL) return 2;
        } else if (strcmp(argv[index], "-config") == 0) {
            config_path = next_argument(argc, argv, &index, "-config");
            if (config_path == NULL) return 2;
        } else if (strcmp(argv[index], "-license") == 0) {
            if (next_argument(argc, argv, &index, "-license") == NULL) return 2;
        } else if (strcmp(argv[index], "-no-watch") == 0) {
            no_watch = 1;
        } else {
            usage(stderr);
            return 2;
        }
    }
    if (show_version) {
        printf("clambhook %s\n", CLAMBHOOK_VERSION);
        return 0;
    }
    if (!allow_incomplete) {
        usage(stderr);
        fprintf(stderr, "native daemon cutover is not enabled until the parity gate passes\n");
        return 2;
    }

    if (config_path[0] != '\0' && !api_address_explicit) {
        ch_config *config = NULL;
        ch_error config_error;
        if (ch_config_load(config_path, &config, &config_error) != CH_OK) {
            fprintf(stderr, "load config: %s\n", config_error.message);
            return 1;
        }
        const ch_config_table *profile = ch_config_active_profile(config);
        const ch_config_table *api = ch_config_table_get_table(profile, "api");
        if (api != NULL &&
            ch_config_table_get_string(api, "listen", &configured_api_address,
                                       &config_error) == CH_OK &&
            configured_api_address[0] != '\0') {
            api_address = configured_api_address;
        } else {
            free(configured_api_address);
            configured_api_address = NULL;
        }
        ch_config_free(config);
    }

    ch_error error;
    daemon_state state = {0};
    state.runtime = ch_runtime_create(NULL, &error);
    if (state.runtime == NULL) {
        fprintf(stderr, "create runtime: %s\n", error.message);
        free(configured_api_address);
        return 1;
    }
    if (ch_runtime_start(state.runtime, config_path, &error) != CH_OK) {
        fprintf(stderr, "start runtime: %s\n", error.message);
        ch_runtime_destroy(state.runtime);
        free(configured_api_address);
        return 1;
    }
    uv_loop_t *loop = uv_default_loop();
    if (config_path[0] != '\0' && !no_watch) {
        ch_config_watcher_options watcher_options = {
            .debounce_milliseconds = 250U,
            .reload = reload_daemon_config,
            .event = report_reload,
            .context = &state
        };
        state.config_watcher = ch_config_watcher_start(
            loop, config_path, &watcher_options, &error
        );
        if (state.config_watcher == NULL) {
            fprintf(stderr, "start config watcher: %s\n", error.message);
            ch_runtime_destroy(state.runtime);
            free(configured_api_address);
            return 1;
        }
    }
    state.api_server = ch_api_server_start(
        loop, state.runtime, api_address, api_token == NULL ? "" : api_token, &error
    );
    free(configured_api_address);
    if (state.api_server == NULL) {
        fprintf(stderr, "start API: %s\n", error.message);
        ch_config_watcher_stop(state.config_watcher);
        state.config_watcher = NULL;
        (void)uv_run(loop, UV_RUN_NOWAIT);
        ch_runtime_destroy(state.runtime);
        return 1;
    }
    (void)uv_signal_init(loop, &state.interrupt_signal);
    (void)uv_signal_init(loop, &state.terminate_signal);
    state.interrupt_signal.data = &state;
    state.terminate_signal.data = &state;
    (void)uv_signal_start(&state.interrupt_signal, stop_daemon, SIGINT);
    (void)uv_signal_start(&state.terminate_signal, stop_daemon, SIGTERM);
    fprintf(
        stderr,
        "clambhook %s experimental native API listening on %s\n",
        CLAMBHOOK_VERSION,
        ch_api_server_address(state.api_server)
    );
    (void)uv_run(loop, UV_RUN_DEFAULT);
    ch_runtime_destroy(state.runtime);
    (void)uv_loop_close(loop);
    return 0;
}
