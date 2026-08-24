#include "clambhook/runtime.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "internal.h"

struct ch_runtime {
    pthread_mutex_t mutex;
    bool running;
    ch_config *config;
    char *config_path;
    char *active_profile;
    ch_runtime_options options;
};

static ch_status android_runtime_apply_config(ch_runtime *runtime,
                                              const char *config_path,
                                              ch_error *error) {
    ch_config *config = NULL;
    char *path;
    char *active;
    if (config_path == NULL) config_path = "";
    path = ch_strdup(config_path);
    if (path == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (config_path[0] == '\0') {
        active = ch_strdup("default");
    } else {
        ch_status status = ch_config_load(config_path, &config, error);
        if (status != CH_OK) {
            free(path);
            return status;
        }
        const ch_config_table *profile = ch_config_active_profile(config);
        if (profile == NULL ||
            ch_config_table_get_string(profile, "name", &active, error) != CH_OK) {
            ch_config_free(config);
            free(path);
            ch_error_set(error, CH_ERROR_PARSE, "active profile has no name");
            return CH_ERROR_PARSE;
        }
    }
    if (active == NULL) {
        ch_config_free(config);
        free(path);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy active profile");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_config_free(runtime->config);
    free(runtime->config_path);
    free(runtime->active_profile);
    runtime->config = config;
    runtime->config_path = path;
    runtime->active_profile = active;
    return CH_OK;
}

static char *android_runtime_status_json(const ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append_format(&json, "{\"running\":%s,\"profile\":",
                               runtime->running ? "true" : "false") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, ",\"network_info\":{}}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static char *android_runtime_profiles_json(const ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append(&json, "{\"profiles\":[")) goto failure;
    if (runtime->config == NULL) {
        if (!ch_json_append_string(&json, runtime->active_profile)) goto failure;
    } else {
        size_t count = ch_config_profile_count(runtime->config);
        for (size_t index = 0U; index < count; ++index) {
            char *name = NULL;
            ch_error error;
            if ((index > 0U && !ch_json_append(&json, ",")) ||
                ch_config_table_get_string(ch_config_profile_at(runtime->config, index),
                                           "name", &name, &error) != CH_OK ||
                !ch_json_append_string(&json, name)) {
                free(name);
                goto failure;
            }
            free(name);
        }
    }
    if (!ch_json_append(&json, "],\"active\":") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, "}")) goto failure;
    return ch_json_take(&json);

failure:
    ch_json_dispose(&json);
    return NULL;
}

ch_runtime *ch_runtime_create(const ch_runtime_options *options, ch_error *error) {
    ch_runtime *runtime;
    ch_error_clear(error);
    runtime = calloc(1U, sizeof(*runtime));
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime");
        return NULL;
    }
    if (pthread_mutex_init(&runtime->mutex, NULL) != 0) {
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime mutex");
        return NULL;
    }
    runtime->active_profile = ch_strdup("default");
    if (runtime->active_profile == NULL) {
        pthread_mutex_destroy(&runtime->mutex);
        free(runtime);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate default profile");
        return NULL;
    }
    if (options != NULL) runtime->options = *options;
    return runtime;
}

void ch_runtime_destroy(ch_runtime *runtime) {
    if (runtime == NULL) return;
    pthread_mutex_lock(&runtime->mutex);
    runtime->running = false;
    ch_config_free(runtime->config);
    runtime->config = NULL;
    free(runtime->config_path);
    free(runtime->active_profile);
    pthread_mutex_unlock(&runtime->mutex);
    pthread_mutex_destroy(&runtime->mutex);
    free(runtime);
}

ch_status ch_runtime_start(ch_runtime *runtime, const char *config_path, ch_error *error) {
    ch_status status;
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    if (runtime->running) {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_INVALID_STATE, "engine already running");
        return CH_ERROR_INVALID_STATE;
    }
    status = android_runtime_apply_config(runtime, config_path, error);
    if (status == CH_OK) runtime->running = true;
    pthread_mutex_unlock(&runtime->mutex);
    return status;
}

ch_status ch_runtime_stop(ch_runtime *runtime, ch_error *error) {
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    runtime->running = false;
    pthread_mutex_unlock(&runtime->mutex);
    return CH_OK;
}

ch_status ch_runtime_reload(ch_runtime *runtime, const char *config_path, ch_error *error) {
    ch_status status;
    ch_error_clear(error);
    if (runtime == NULL || config_path == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime and config path are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    status = android_runtime_apply_config(runtime, config_path, error);
    pthread_mutex_unlock(&runtime->mutex);
    return status;
}

ch_status ch_runtime_inject_packet(ch_runtime *runtime, const uint8_t *packet,
                                   size_t length, ch_error *error) {
    ch_error_clear(error);
    if (runtime == NULL || packet == NULL || length == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime and non-empty packet are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    bool running = runtime->running;
    pthread_mutex_unlock(&runtime->mutex);
    ch_error_set(error, running ? CH_ERROR_UNSUPPORTED : CH_ERROR_INVALID_STATE,
                 running ? "userspace packet stack has not been enabled in this migration phase"
                         : "runtime is not running");
    return running ? CH_ERROR_UNSUPPORTED : CH_ERROR_INVALID_STATE;
}

bool ch_runtime_is_running(ch_runtime *runtime) {
    bool running;
    if (runtime == NULL) return false;
    pthread_mutex_lock(&runtime->mutex);
    running = runtime->running;
    pthread_mutex_unlock(&runtime->mutex);
    return running;
}

ch_status ch_runtime_query(ch_runtime *runtime, const char *operation,
                           const char *request_json, char **response_json,
                           ch_error *error) {
    char *response = NULL;
    ch_error_clear(error);
    if (runtime == NULL || operation == NULL || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime, operation, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    pthread_mutex_lock(&runtime->mutex);
    if (strcmp(operation, "status") == 0) response = android_runtime_status_json(runtime);
    else if (strcmp(operation, "profiles") == 0) response = android_runtime_profiles_json(runtime);
    else if (strcmp(operation, "servers") == 0) {
        response = ch_config_servers_payload_json(runtime->config,
            runtime->active_profile, error);
    } else if (strcmp(operation, "rules") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "rule", "rules", 1, 0, error);
    } else if (strcmp(operation, "policy_groups") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "policy_group", "groups", 0, 0, error);
    } else if (strcmp(operation, "rule_sets") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "rule_set", "rule_sets", 0, 1, error);
    } else if (strcmp(operation, "config") == 0) {
        response = ch_config_profile_payload_json(runtime->config,
            runtime->active_profile, error);
    } else if (strcmp(operation, "test_rule") == 0) {
        ch_status status = ch_rule_explain_request_json(runtime->config,
            runtime->active_profile, request_json, &response, error);
        if (status != CH_OK) {
            pthread_mutex_unlock(&runtime->mutex);
            return status;
        }
    }
    else {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_UNSUPPORTED, "unknown runtime query operation");
        return CH_ERROR_UNSUPPORTED;
    }
    pthread_mutex_unlock(&runtime->mutex);
    if (response == NULL) {
        ch_status status = error == NULL || error->code == CH_OK
            ? CH_ERROR_OUT_OF_MEMORY : error->code;
        if (status == CH_ERROR_OUT_OF_MEMORY) {
            ch_error_set(error, status, "encode query response");
        }
        return status;
    }
    *response_json = response;
    return CH_OK;
}

ch_status ch_runtime_mutate(ch_runtime *runtime, const char *operation,
                            const char *request_json, char **response_json,
                            ch_error *error) {
    (void)request_json;
    ch_error_clear(error);
    if (runtime == NULL || operation == NULL || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime, operation, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    if (strcmp(operation, "connect") == 0) runtime->running = true;
    else if (strcmp(operation, "disconnect") == 0) runtime->running = false;
    else if (strcmp(operation, "set_active_profile") == 0) {
        char *name = ch_json_request_string(request_json, "name", error);
        if (name == NULL) {
            pthread_mutex_unlock(&runtime->mutex);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        if (runtime->config == NULL || !ch_config_has_profile(runtime->config, name)) {
            pthread_mutex_unlock(&runtime->mutex);
            ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found", name);
            free(name);
            return CH_ERROR_NOT_FOUND;
        }
        free(runtime->active_profile);
        runtime->active_profile = name;
    }
    else {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_UNSUPPORTED, "unknown runtime mutation operation");
        return CH_ERROR_UNSUPPORTED;
    }
    *response_json = android_runtime_status_json(runtime);
    pthread_mutex_unlock(&runtime->mutex);
    if (*response_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode mutation response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

void ch_string_free(char *string) {
    free(string);
}
