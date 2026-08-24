#include "clambhook/runtime.h"

#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <uv.h>

#include "clambhook/config.h"
#include "internal.h"

typedef enum ch_command_kind {
    CH_COMMAND_START,
    CH_COMMAND_STOP,
    CH_COMMAND_RELOAD,
    CH_COMMAND_INJECT,
    CH_COMMAND_QUERY,
    CH_COMMAND_MUTATE,
    CH_COMMAND_SHUTDOWN
} ch_command_kind;

typedef struct ch_command {
    ch_command_kind kind;
    char *operation;
    char *payload;
    uint8_t *packet;
    size_t packet_length;
    char *response;
    ch_status status;
    ch_error error;
    uv_mutex_t mutex;
    uv_cond_t condition;
    int complete;
    struct ch_command *next;
} ch_command;

struct ch_runtime {
    uv_loop_t loop;
    uv_async_t command_async;
    uv_thread_t thread;
    uv_mutex_t queue_mutex;
    ch_command *queue_head;
    ch_command *queue_tail;
    atomic_bool running;
    ch_runtime_listener_set *listeners;
    ch_config *config;
    char *config_path;
    char *active_profile;
    ch_runtime_options options;
};

static void ch_runtime_log(ch_runtime *runtime, int level, const char *message) {
    if (runtime->options.log_writer != NULL) {
        runtime->options.log_writer(level, message, runtime->options.log_writer_context);
    }
}

static char *ch_runtime_status_json(ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append_format(
            &json,
            "{\"running\":%s,\"profile\":",
            atomic_load_explicit(&runtime->running, memory_order_acquire) ? "true" : "false"
        ) ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, ",\"network_info\":{}") ||
        !ch_runtime_listener_set_append_status(runtime->listeners, &json) ||
        !ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static char *ch_runtime_profiles_json(ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append(&json, "{\"profiles\":[")) {
        ch_json_dispose(&json);
        return NULL;
    }
    if (runtime->config == NULL) {
        if (!ch_json_append_string(&json, runtime->active_profile)) {
            ch_json_dispose(&json);
            return NULL;
        }
    } else {
        size_t count = ch_config_profile_count(runtime->config);
        for (size_t index = 0U; index < count; ++index) {
            const ch_config_table *profile = ch_config_profile_at(runtime->config, index);
            char *name = NULL;
            ch_error error;
            if ((index > 0U && !ch_json_append(&json, ",")) ||
                ch_config_table_get_string(profile, "name", &name, &error) != CH_OK ||
                !ch_json_append_string(&json, name)) {
                free(name);
                ch_json_dispose(&json);
                return NULL;
            }
            free(name);
        }
    }
    if (!ch_json_append(&json, "],\"active\":") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static void ch_command_fail(ch_command *command, ch_status status, const char *message) {
    command->status = status;
    ch_error_set(&command->error, status, "%s", message);
}

static void ch_runtime_stop_listeners(ch_runtime *runtime) {
    ch_runtime_listener_set_stop(runtime->listeners);
    runtime->listeners = NULL;
}

static bool ch_runtime_start_listeners(ch_runtime *runtime,
                                       const ch_config *config,
                                       const char *profile_name,
                                       ch_command *command) {
    ch_error listener_error;
    ch_runtime_listener_set *listeners = ch_runtime_listener_set_start(
        config, profile_name, &listener_error
    );
    if (listeners == NULL) {
        command->status = listener_error.code;
        command->error = listener_error;
        return false;
    }
    runtime->listeners = listeners;
    return true;
}

static bool ch_runtime_apply_config(ch_runtime *runtime, ch_command *command,
                                    const char *path, bool start_listeners) {
    ch_config *next_config = NULL;
    char *next_path;
    char *next_profile;
    if (path == NULL) {
        path = "";
    }
    next_path = ch_strdup(path);
    if (next_path == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return false;
    }
    if (path[0] != '\0') {
        ch_error config_error;
        ch_status status = ch_config_load(path, &next_config, &config_error);
        if (status != CH_OK) {
            free(next_path);
            command->status = status;
            command->error = config_error;
            return false;
        }
        {
            const ch_config_table *profile = ch_config_active_profile(next_config);
            ch_error name_error;
            if (profile == NULL ||
                ch_config_table_get_string(profile, "name", &next_profile, &name_error) != CH_OK) {
                ch_config_free(next_config);
                free(next_path);
                ch_command_fail(command, CH_ERROR_PARSE, "active profile has no name");
                return false;
            }
        }
    } else {
        next_profile = ch_strdup("default");
        if (next_profile == NULL) {
            free(next_path);
            ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy default profile");
            return false;
        }
    }
    ch_runtime_listener_set *next_listeners = NULL;
    if (start_listeners) {
        ch_runtime_stop_listeners(runtime);
        ch_error listener_error;
        next_listeners = ch_runtime_listener_set_start(
            next_config, next_profile, &listener_error
        );
        if (next_listeners == NULL) {
            ch_error rollback_error;
            runtime->listeners = ch_runtime_listener_set_start(
                runtime->config, runtime->active_profile, &rollback_error
            );
            ch_config_free(next_config);
            free(next_path);
            free(next_profile);
            command->status = listener_error.code;
            command->error = listener_error;
            return false;
        }
    }
    ch_config_free(runtime->config);
    free(runtime->config_path);
    free(runtime->active_profile);
    runtime->config = next_config;
    runtime->config_path = next_path;
    runtime->active_profile = next_profile;
    runtime->listeners = next_listeners;
    return true;
}

static void ch_command_process(ch_runtime *runtime, ch_command *command) {
    command->status = CH_OK;
    ch_error_clear(&command->error);

    switch (command->kind) {
        case CH_COMMAND_START:
            if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                ch_command_fail(command, CH_ERROR_INVALID_STATE, "engine already running");
                break;
            }
            if (!ch_runtime_apply_config(runtime, command, command->payload, true)) {
                break;
            }
            atomic_store_explicit(&runtime->running, true, memory_order_release);
            ch_runtime_log(runtime, 1, "native runtime started");
            break;

        case CH_COMMAND_STOP:
            ch_runtime_stop_listeners(runtime);
            atomic_store_explicit(&runtime->running, false, memory_order_release);
            ch_runtime_log(runtime, 1, "native runtime stopped");
            break;

        case CH_COMMAND_RELOAD: {
            bool running = atomic_load_explicit(&runtime->running, memory_order_acquire);
            (void)ch_runtime_apply_config(runtime, command, command->payload, running);
            break;
        }

        case CH_COMMAND_INJECT:
            if (!atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                ch_command_fail(command, CH_ERROR_INVALID_STATE, "runtime is not running");
            } else {
                ch_command_fail(
                    command,
                    CH_ERROR_UNSUPPORTED,
                    "userspace packet stack has not been enabled in this migration phase"
                );
            }
            break;

        case CH_COMMAND_QUERY:
            if (strcmp(command->operation, "status") == 0) {
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "profiles") == 0) {
                command->response = ch_runtime_profiles_json(runtime);
            } else if (strcmp(command->operation, "servers") == 0) {
                command->response = ch_config_servers_payload_json(
                    runtime->config, runtime->active_profile, &command->error
                );
            } else if (strcmp(command->operation, "rules") == 0) {
                command->response = ch_config_collection_payload_json(
                    runtime->config, runtime->active_profile, "rule", "rules", 1, 0,
                    &command->error
                );
            } else if (strcmp(command->operation, "policy_groups") == 0) {
                command->response = ch_config_collection_payload_json(
                    runtime->config, runtime->active_profile, "policy_group", "groups", 0, 0,
                    &command->error
                );
            } else if (strcmp(command->operation, "rule_sets") == 0) {
                command->response = ch_config_collection_payload_json(
                    runtime->config, runtime->active_profile, "rule_set", "rule_sets", 0, 1,
                    &command->error
                );
            } else if (strcmp(command->operation, "config") == 0) {
                command->response = ch_config_profile_payload_json(
                    runtime->config, runtime->active_profile, &command->error
                );
            } else if (strcmp(command->operation, "test_rule") == 0) {
                command->status = ch_rule_explain_request_json(
                    runtime->config, runtime->active_profile, command->payload,
                    &command->response, &command->error
                );
            } else {
                ch_command_fail(command, CH_ERROR_UNSUPPORTED, "unknown runtime query operation");
                break;
            }
            if (command->response == NULL && command->status == CH_OK) {
                if (command->error.code == CH_OK) {
                    ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "encode query response");
                } else {
                    command->status = command->error.code;
                }
            }
            break;

        case CH_COMMAND_MUTATE:
            if (strcmp(command->operation, "connect") == 0) {
                if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                    ch_command_fail(command, CH_ERROR_INVALID_STATE, "engine already running");
                    break;
                }
                if (!ch_runtime_start_listeners(runtime, runtime->config,
                                                runtime->active_profile, command)) {
                    break;
                }
                atomic_store_explicit(&runtime->running, true, memory_order_release);
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "disconnect") == 0) {
                ch_runtime_stop_listeners(runtime);
                atomic_store_explicit(&runtime->running, false, memory_order_release);
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "set_active_profile") == 0) {
                char *name = ch_json_request_string(command->payload, "name", &command->error);
                if (name == NULL) {
                    command->status = command->error.code;
                    break;
                }
                if (runtime->config == NULL || !ch_config_has_profile(runtime->config, name)) {
                    ch_error_set(&command->error, CH_ERROR_NOT_FOUND,
                                 "profile %s not found", name);
                    command->status = CH_ERROR_NOT_FOUND;
                    free(name);
                    break;
                }
                if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                    char *old_name = runtime->active_profile;
                    ch_runtime_stop_listeners(runtime);
                    if (!ch_runtime_start_listeners(runtime, runtime->config,
                                                    name, command)) {
                        ch_error profile_error = command->error;
                        ch_status profile_status = command->status;
                        ch_error rollback_error;
                        runtime->listeners = ch_runtime_listener_set_start(
                            runtime->config, old_name, &rollback_error
                        );
                        free(name);
                        command->status = profile_status;
                        command->error = profile_error;
                        break;
                    }
                    runtime->active_profile = name;
                    free(old_name);
                } else {
                    free(runtime->active_profile);
                    runtime->active_profile = name;
                }
                command->response = ch_runtime_status_json(runtime);
            } else {
                ch_command_fail(command, CH_ERROR_UNSUPPORTED, "unknown runtime mutation operation");
                break;
            }
            if (command->response == NULL && command->status == CH_OK) {
                ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "encode mutation response");
            }
            break;

        case CH_COMMAND_SHUTDOWN:
            ch_runtime_stop_listeners(runtime);
            atomic_store_explicit(&runtime->running, false, memory_order_release);
            uv_close((uv_handle_t *)&runtime->command_async, NULL);
            break;
    }
}

static void ch_command_complete(ch_command *command) {
    uv_mutex_lock(&command->mutex);
    command->complete = 1;
    uv_cond_signal(&command->condition);
    uv_mutex_unlock(&command->mutex);
}

static void ch_runtime_drain_commands(uv_async_t *async) {
    ch_runtime *runtime = async->data;
    for (;;) {
        uv_mutex_lock(&runtime->queue_mutex);
        ch_command *command = runtime->queue_head;
        if (command != NULL) {
            runtime->queue_head = command->next;
            if (runtime->queue_head == NULL) {
                runtime->queue_tail = NULL;
            }
        }
        uv_mutex_unlock(&runtime->queue_mutex);
        if (command == NULL) {
            return;
        }
        ch_command_process(runtime, command);
        ch_command_complete(command);
    }
}

static void ch_runtime_thread(void *context) {
    ch_runtime *runtime = context;
    (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
}

static void ch_command_dispose(ch_command *command) {
    if (command == NULL) {
        return;
    }
    uv_cond_destroy(&command->condition);
    uv_mutex_destroy(&command->mutex);
    free(command->operation);
    free(command->payload);
    free(command->packet);
}

static ch_status ch_runtime_dispatch(
    ch_runtime *runtime,
    ch_command_kind kind,
    const char *operation,
    const char *payload,
    const uint8_t *packet,
    size_t packet_length,
    char **response,
    ch_error *error
) {
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (response != NULL) {
        *response = NULL;
    }

    ch_command command;
    memset(&command, 0, sizeof(command));
    command.kind = kind;
    command.operation = operation == NULL ? NULL : ch_strdup(operation);
    command.payload = payload == NULL ? NULL : ch_strdup(payload);
    if ((operation != NULL && command.operation == NULL) ||
        (payload != NULL && command.payload == NULL)) {
        free(command.operation);
        free(command.payload);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime command");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (packet_length > 0U) {
        command.packet = malloc(packet_length);
        if (command.packet == NULL) {
            free(command.operation);
            free(command.payload);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate packet command");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        memcpy(command.packet, packet, packet_length);
        command.packet_length = packet_length;
    }
    if (uv_mutex_init(&command.mutex) != 0) {
        free(command.operation);
        free(command.payload);
        free(command.packet);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize command mutex");
        return CH_ERROR_INTERNAL;
    }
    if (uv_cond_init(&command.condition) != 0) {
        uv_mutex_destroy(&command.mutex);
        free(command.operation);
        free(command.payload);
        free(command.packet);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize command condition");
        return CH_ERROR_INTERNAL;
    }

    uv_mutex_lock(&command.mutex);
    uv_mutex_lock(&runtime->queue_mutex);
    if (runtime->queue_tail == NULL) {
        runtime->queue_head = &command;
    } else {
        runtime->queue_tail->next = &command;
    }
    runtime->queue_tail = &command;
    uv_mutex_unlock(&runtime->queue_mutex);
    (void)uv_async_send(&runtime->command_async);
    while (!command.complete) {
        uv_cond_wait(&command.condition, &command.mutex);
    }
    uv_mutex_unlock(&command.mutex);

    ch_status status = command.status;
    if (error != NULL) {
        *error = command.error;
    }
    if (response != NULL) {
        *response = command.response;
        command.response = NULL;
    }
    free(command.response);
    ch_command_dispose(&command);
    return status;
}

ch_runtime *ch_runtime_create(const ch_runtime_options *options, ch_error *error) {
    ch_error_clear(error);
    ch_runtime *runtime = calloc(1U, sizeof(*runtime));
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime");
        return NULL;
    }
    runtime->active_profile = ch_strdup("default");
    if (runtime->active_profile == NULL) {
        free(runtime);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate default profile");
        return NULL;
    }
    if (options != NULL) {
        runtime->options = *options;
    }
    atomic_init(&runtime->running, false);
    if (uv_loop_init(&runtime->loop) != 0 || uv_mutex_init(&runtime->queue_mutex) != 0) {
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime loop");
        return NULL;
    }
    if (uv_async_init(&runtime->loop, &runtime->command_async, ch_runtime_drain_commands) != 0) {
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime command queue");
        return NULL;
    }
    runtime->command_async.data = runtime;
    if (uv_thread_create(&runtime->thread, ch_runtime_thread, runtime) != 0) {
        uv_close((uv_handle_t *)&runtime->command_async, NULL);
        (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "start runtime thread");
        return NULL;
    }
    return runtime;
}

void ch_runtime_destroy(ch_runtime *runtime) {
    if (runtime == NULL) {
        return;
    }
    ch_error ignored;
    (void)ch_runtime_dispatch(runtime, CH_COMMAND_SHUTDOWN, NULL, NULL, NULL, 0U, NULL, &ignored);
    (void)uv_thread_join(&runtime->thread);
    uv_mutex_destroy(&runtime->queue_mutex);
    (void)uv_loop_close(&runtime->loop);
    free(runtime->config_path);
    ch_config_free(runtime->config);
    free(runtime->active_profile);
    free(runtime);
}

ch_status ch_runtime_start(ch_runtime *runtime, const char *config_path, ch_error *error) {
    if (config_path == NULL) {
        config_path = "";
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_START, NULL, config_path, NULL, 0U, NULL, error);
}

ch_status ch_runtime_stop(ch_runtime *runtime, ch_error *error) {
    return ch_runtime_dispatch(runtime, CH_COMMAND_STOP, NULL, NULL, NULL, 0U, NULL, error);
}

ch_status ch_runtime_reload(ch_runtime *runtime, const char *config_path, ch_error *error) {
    if (config_path == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config path is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_RELOAD, NULL, config_path, NULL, 0U, NULL, error);
}

ch_status ch_runtime_inject_packet(
    ch_runtime *runtime,
    const uint8_t *packet,
    size_t length,
    ch_error *error
) {
    if (packet == NULL || length == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "non-empty packet is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_INJECT, NULL, NULL, packet, length, NULL, error);
}

bool ch_runtime_is_running(ch_runtime *runtime) {
    return runtime != NULL && atomic_load_explicit(&runtime->running, memory_order_acquire);
}

ch_status ch_runtime_query(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
) {
    if (operation == NULL || operation[0] == '\0' || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "operation and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(
        runtime,
        CH_COMMAND_QUERY,
        operation,
        request_json == NULL ? "{}" : request_json,
        NULL,
        0U,
        response_json,
        error
    );
}

ch_status ch_runtime_mutate(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
) {
    if (operation == NULL || operation[0] == '\0' || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "operation and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(
        runtime,
        CH_COMMAND_MUTATE,
        operation,
        request_json == NULL ? "{}" : request_json,
        NULL,
        0U,
        response_json,
        error
    );
}

void ch_string_free(char *string) {
    free(string);
}
