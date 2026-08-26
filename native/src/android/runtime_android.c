#include "clambhook/runtime.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "clambhook/config.h"
#include "clambhook/ip_stack.h"
#include "clambhook/protocol.h"
#include "clambhook/rules.h"
#include "cnet.h"
#include "internal.h"

typedef struct android_route {
    bool direct;
    const ch_config_table *chain;
    char *selected_group_chain;
} android_route;

typedef struct android_config_state {
    ch_config *config;
    ch_rule_engine *rules;
    char *config_path;
    char *active_profile;
} android_config_state;

struct ch_runtime {
    pthread_mutex_t mutex;
    pthread_mutex_t ip_mutex;
    pthread_t tick_thread;
    atomic_bool tick_stop;
    bool tick_started;
    bool running;
    ch_config *config;
    ch_rule_engine *rules;
    char *config_path;
    char *active_profile;
    ch_ip_stack *ip_stack;
    ch_runtime_options options;
};

static char *android_optional_string(const ch_config_table *table,
                                     const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static const ch_config_table *android_named_table(
    const ch_config_array *array, const char *name) {
    size_t count = ch_config_array_count(array);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        char *candidate = android_optional_string(table, "name");
        int matches = candidate != NULL && strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return table;
    }
    return NULL;
}

static const char *android_selected_group_chain(
    const ch_config_table *profile, const char *group_name,
    char **out_chain) {
    *out_chain = NULL;
    const ch_config_table *group = android_named_table(
        ch_config_table_get_array(profile, "policy_group"), group_name);
    if (group == NULL) return NULL;
    *out_chain = android_optional_string(group, "selected");
    if (*out_chain != NULL && (*out_chain)[0] != '\0') return *out_chain;
    free(*out_chain);
    *out_chain = NULL;
    const ch_config_array *chains = ch_config_table_get_array(group, "chains");
    if (ch_config_array_count(chains) == 0U) return NULL;
    ch_error ignored;
    if (ch_config_array_get_string(chains, 0U, out_chain, &ignored) != CH_OK) {
        free(*out_chain);
        *out_chain = NULL;
    }
    return *out_chain;
}

static void android_route_clear(android_route *route) {
    if (route == NULL) return;
    free(route->selected_group_chain);
    memset(route, 0, sizeof(*route));
}

static ch_status android_runtime_resolve_route(
    ch_runtime *runtime, const char *network, const char *target,
    const char *source, const char *domain_hint, android_route *out_route,
    ch_error *error) {
    memset(out_route, 0, sizeof(*out_route));
    out_route->direct = true;
    if (runtime->rules == NULL) return CH_OK;
    ch_rule_match_context context = {
        .network = network,
        .target = domain_hint != NULL && domain_hint[0] != '\0' ?
            domain_hint : target,
        .source = source,
        .process_name = "",
        .process_path = ""
    };
    ch_rule_decision decision;
    ch_status status = ch_rule_engine_decide(runtime->rules, &context,
                                             &decision, error);
    if (status != CH_OK) return status;
    const char *chain_name = decision.chain_name;
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0 ||
        strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "Android native route rejected flow");
        status = CH_ERROR_INVALID_STATE;
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        status = CH_OK;
    } else if (strcmp(decision.action, CH_RULE_ACTION_GROUP) == 0) {
        const ch_config_table *profile = ch_config_profile_named(
            runtime->config, runtime->active_profile);
        chain_name = android_selected_group_chain(
            profile, decision.group_name, &out_route->selected_group_chain);
        if (chain_name == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "Android native policy group has no selected chain");
            status = CH_ERROR_NOT_FOUND;
        }
    } else if (strcmp(decision.action, CH_RULE_ACTION_CHAIN) != 0) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "Android native route action is not supported");
        status = CH_ERROR_UNSUPPORTED;
    }
    if (status == CH_OK &&
        strcmp(decision.action, CH_RULE_ACTION_DIRECT) != 0) {
        const ch_config_table *profile = ch_config_profile_named(
            runtime->config, runtime->active_profile);
        out_route->chain = android_named_table(
            ch_config_table_get_array(profile, "chain"), chain_name);
        if (out_route->chain == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "Android native route chain was not found");
            status = CH_ERROR_NOT_FOUND;
        } else {
            out_route->direct = false;
        }
    }
    ch_rule_decision_clear(&decision);
    if (status != CH_OK) android_route_clear(out_route);
    return status;
}

static ch_status android_runtime_tcp_dial(
    const char *target, const char *source, const char *domain_hint,
    int *out_descriptor, void *context, ch_error *error) {
    ch_runtime *runtime = context;
    android_route route;
    ch_status status = android_runtime_resolve_route(
        runtime, "tcp", target, source, domain_hint, &route, error);
    if (status == CH_OK) {
        status = route.direct ?
            ch_protocol_connect_tcp(target, out_descriptor, error) :
            ch_protocol_chain_dial(route.chain, "tcp", target,
                                   out_descriptor, error);
    }
    android_route_clear(&route);
    return status;
}

static ch_status android_runtime_udp_dial(
    const char *target, const char *source, const char *domain_hint,
    void **out_connection, void *context, ch_error *error) {
    ch_runtime *runtime = context;
    *out_connection = NULL;
    android_route route;
    ch_status status = android_runtime_resolve_route(
        runtime, "udp", target, source, domain_hint, &route, error);
    if (status == CH_OK) {
        ch_packet_connection *connection = NULL;
        status = route.direct ?
            ch_protocol_direct_packet_dial(&connection, error) :
            ch_protocol_chain_dial_packet(route.chain, target,
                                          &connection, error);
        if (status == CH_OK) *out_connection = connection;
    }
    android_route_clear(&route);
    return status;
}

static ch_status android_runtime_udp_send(
    void *opaque, const char *target, const uint8_t *payload,
    size_t payload_length, ch_error *error) {
    return ch_packet_connection_send(opaque, target, payload,
                                     payload_length, error);
}

static ch_status android_runtime_udp_receive(
    void *opaque, uint8_t *buffer, size_t buffer_capacity,
    size_t *out_length, char **out_source, ch_error *error) {
    return ch_packet_connection_receive_timeout(
        opaque, buffer, buffer_capacity, out_length, out_source, 0, error);
}

static void android_runtime_udp_close(void *opaque) {
    ch_packet_connection_close(opaque);
}

static void *android_runtime_tick_main(void *context) {
    ch_runtime *runtime = context;
    const struct timespec interval = {.tv_sec = 0, .tv_nsec = 10000000L};
    while (!atomic_load_explicit(&runtime->tick_stop,
                                 memory_order_acquire)) {
        struct timespec remaining = interval;
        while (nanosleep(&remaining, &remaining) != 0) {
            if (atomic_load_explicit(&runtime->tick_stop,
                                     memory_order_acquire)) {
                return NULL;
            }
        }
        pthread_mutex_lock(&runtime->ip_mutex);
        ch_ip_stack_tick(runtime->ip_stack);
        pthread_mutex_unlock(&runtime->ip_mutex);
    }
    return NULL;
}

static void android_runtime_write_packet(const uint8_t *packet, size_t length,
                                         void *context) {
    ch_runtime *runtime = context;
    runtime->options.packet_writer(packet, length,
                                   runtime->options.packet_writer_context);
}

static ch_status android_runtime_start_ip_stack(ch_runtime *runtime,
                                                ch_error *error) {
    if (runtime->options.packet_writer == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Android runtime packet writer is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_ip_stack_options options = {
        .packet_writer = android_runtime_write_packet,
        .packet_writer_context = runtime,
        .tcp_dialer = android_runtime_tcp_dial,
        .tcp_dialer_context = runtime,
        .udp_dialer = android_runtime_udp_dial,
        .udp_dialer_context = runtime,
        .udp_sender = android_runtime_udp_send,
        .udp_receiver = android_runtime_udp_receive,
        .udp_closer = android_runtime_udp_close
    };
    pthread_mutex_lock(&runtime->ip_mutex);
    runtime->ip_stack = ch_ip_stack_create(&options, error);
    pthread_mutex_unlock(&runtime->ip_mutex);
    if (runtime->ip_stack == NULL) return error->code;
    atomic_store_explicit(&runtime->tick_stop, false, memory_order_release);
    if (pthread_create(&runtime->tick_thread, NULL,
                       android_runtime_tick_main, runtime) != 0) {
        pthread_mutex_lock(&runtime->ip_mutex);
        ch_ip_stack_destroy(runtime->ip_stack);
        runtime->ip_stack = NULL;
        pthread_mutex_unlock(&runtime->ip_mutex);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "start Android packet timer");
        return CH_ERROR_INTERNAL;
    }
    runtime->tick_started = true;
    return CH_OK;
}

static void android_runtime_stop_ip_stack(ch_runtime *runtime) {
    atomic_store_explicit(&runtime->tick_stop, true, memory_order_release);
    if (runtime->tick_started) {
        (void)pthread_join(runtime->tick_thread, NULL);
        runtime->tick_started = false;
    }
    pthread_mutex_lock(&runtime->ip_mutex);
    ch_ip_stack_destroy(runtime->ip_stack);
    runtime->ip_stack = NULL;
    pthread_mutex_unlock(&runtime->ip_mutex);
}

static void android_config_state_clear(android_config_state *state) {
    if (state == NULL) return;
    ch_config_free(state->config);
    ch_rule_engine_destroy(state->rules);
    free(state->config_path);
    free(state->active_profile);
    memset(state, 0, sizeof(*state));
}

static android_config_state android_runtime_take_config(ch_runtime *runtime) {
    android_config_state state = {
        .config = runtime->config,
        .rules = runtime->rules,
        .config_path = runtime->config_path,
        .active_profile = runtime->active_profile
    };
    runtime->config = NULL;
    runtime->rules = NULL;
    runtime->config_path = NULL;
    runtime->active_profile = NULL;
    return state;
}

static void android_runtime_install_config(ch_runtime *runtime,
                                           android_config_state *state) {
    runtime->config = state->config;
    runtime->rules = state->rules;
    runtime->config_path = state->config_path;
    runtime->active_profile = state->active_profile;
    memset(state, 0, sizeof(*state));
}

static ch_status android_runtime_load_config(const char *config_path,
                                             android_config_state *state,
                                             ch_error *error) {
    ch_config *config = NULL;
    char *path;
    char *active;
    memset(state, 0, sizeof(*state));
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
    ch_rule_engine *rules = NULL;
    if (config != NULL) {
        rules = ch_rule_engine_compile_config(config, active, error);
        if (rules == NULL) {
            ch_config_free(config);
            free(path);
            free(active);
            return error == NULL || error->code == CH_OK ?
                CH_ERROR_INVALID_ARGUMENT : error->code;
        }
    }
    state->config = config;
    state->rules = rules;
    state->config_path = path;
    state->active_profile = active;
    return CH_OK;
}

static ch_status android_runtime_replace_config(ch_runtime *runtime,
                                                const char *config_path,
                                                bool restart,
                                                ch_error *error) {
    android_config_state next;
    ch_status status = android_runtime_load_config(config_path, &next, error);
    if (status != CH_OK) return status;

    android_config_state previous = android_runtime_take_config(runtime);
    if (restart) android_runtime_stop_ip_stack(runtime);
    android_runtime_install_config(runtime, &next);
    if (restart) status = android_runtime_start_ip_stack(runtime, error);
    if (status == CH_OK) {
        android_config_state_clear(&previous);
        return CH_OK;
    }

    ch_error replacement_error = *error;
    android_config_state failed = android_runtime_take_config(runtime);
    android_runtime_install_config(runtime, &previous);
    ch_error rollback_error;
    ch_status rollback_status = restart ?
        android_runtime_start_ip_stack(runtime, &rollback_error) : CH_OK;
    android_config_state_clear(&failed);
    if (rollback_status != CH_OK) {
        runtime->running = false;
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "replace Android config failed: %s; rollback failed: %s",
                     replacement_error.message, rollback_error.message);
        return CH_ERROR_INTERNAL;
    }
    *error = replacement_error;
    return status;
}

static ch_status android_runtime_select_profile(ch_runtime *runtime,
                                                char *name,
                                                ch_error *error) {
    ch_rule_engine *next_rules = ch_rule_engine_compile_config(
        runtime->config, name, error);
    if (next_rules == NULL) {
        free(name);
        return error == NULL || error->code == CH_OK ?
            CH_ERROR_INVALID_ARGUMENT : error->code;
    }
    if (strcmp(runtime->active_profile, name) == 0) {
        ch_rule_engine_destroy(next_rules);
        free(name);
        return CH_OK;
    }

    bool running = runtime->running;
    char *previous_name = runtime->active_profile;
    ch_rule_engine *previous_rules = runtime->rules;
    if (running) android_runtime_stop_ip_stack(runtime);
    runtime->active_profile = name;
    runtime->rules = next_rules;

    if (running) {
        ch_status status = android_runtime_start_ip_stack(runtime, error);
        if (status != CH_OK) {
            ch_error switch_error = *error;
            runtime->active_profile = previous_name;
            runtime->rules = previous_rules;
            ch_error rollback_error;
            ch_status rollback_status = android_runtime_start_ip_stack(
                runtime, &rollback_error);
            if (rollback_status != CH_OK) {
                runtime->running = false;
                ch_error_set(error, CH_ERROR_INTERNAL,
                             "switch Android profile failed: %s; "
                             "rollback failed: %s",
                             switch_error.message, rollback_error.message);
                status = CH_ERROR_INTERNAL;
            } else {
                *error = switch_error;
            }
            ch_rule_engine_destroy(next_rules);
            free(name);
            return status;
        }
    }

    ch_rule_engine_destroy(previous_rules);
    free(previous_name);
    return CH_OK;
}

static char *android_runtime_status_json(const ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append_format(&json, "{\"running\":%s,\"profile\":",
                               runtime->running ? "true" : "false") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, ",\"network_info\":{}") ||
        (runtime->ip_stack != NULL &&
         !ch_json_append(&json, ",\"tunnel_mode\":\"tun\"")) ||
        !ch_json_append(&json, "}")) {
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

static char *android_runtime_crypto_self_test_json(ch_error *error) {
    static const uint8_t plaintext[] = "clambhook-android-openssl-self-test";
    static const uint8_t aad[] = "native-c17";
    uint8_t key[32] = {0};
    uint8_t nonce[12] = {0};
    uint8_t ciphertext[sizeof(plaintext)];
    uint8_t recovered[sizeof(plaintext)];
    uint8_t tag[16];
    int healthy = cnet_aes128gcm_available() &&
        cnet_aes256gcm_available();
    if (healthy) {
        healthy = cnet_aes128gcm_encrypt(
            key, nonce, plaintext, sizeof(plaintext), aad, sizeof(aad),
            ciphertext, tag) == CNET_OK &&
            cnet_aes128gcm_decrypt(
                key, nonce, ciphertext, sizeof(ciphertext), aad, sizeof(aad),
                tag, recovered) == CNET_OK &&
            memcmp(recovered, plaintext, sizeof(plaintext)) == 0;
    }
    if (healthy) {
        healthy = cnet_aes256gcm_encrypt(
            key, nonce, plaintext, sizeof(plaintext), aad, sizeof(aad),
            ciphertext, tag) == CNET_OK &&
            cnet_aes256gcm_decrypt(
                key, nonce, ciphertext, sizeof(ciphertext), aad, sizeof(aad),
                tag, recovered) == CNET_OK &&
            memcmp(recovered, plaintext, sizeof(plaintext)) == 0;
    }
    if (healthy) {
        healthy = cnet_chacha20poly1305_encrypt(
            key, nonce, plaintext, sizeof(plaintext), aad, sizeof(aad),
            ciphertext, tag) == CNET_OK &&
            cnet_chacha20poly1305_decrypt(
                key, nonce, ciphertext, sizeof(ciphertext), aad, sizeof(aad),
                tag, recovered) == CNET_OK &&
            memcmp(recovered, plaintext, sizeof(plaintext)) == 0;
    }
    memset(key, 0, sizeof(key));
    memset(ciphertext, 0, sizeof(ciphertext));
    memset(recovered, 0, sizeof(recovered));
    memset(tag, 0, sizeof(tag));
    if (!healthy) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "Android native OpenSSL AEAD self-test failed");
        return NULL;
    }
    return ch_strdup(
        "{\"openssl\":\"3.5.8\",\"aes_128_gcm\":true,"
        "\"aes_256_gcm\":true,\"chacha20_poly1305\":true}");
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
    if (pthread_mutex_init(&runtime->ip_mutex, NULL) != 0) {
        pthread_mutex_destroy(&runtime->mutex);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize Android packet mutex");
        return NULL;
    }
    atomic_init(&runtime->tick_stop, true);
    runtime->active_profile = ch_strdup("default");
    if (runtime->active_profile == NULL) {
        pthread_mutex_destroy(&runtime->mutex);
        pthread_mutex_destroy(&runtime->ip_mutex);
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
    android_runtime_stop_ip_stack(runtime);
    ch_config_free(runtime->config);
    ch_rule_engine_destroy(runtime->rules);
    runtime->config = NULL;
    runtime->rules = NULL;
    free(runtime->config_path);
    free(runtime->active_profile);
    pthread_mutex_unlock(&runtime->mutex);
    pthread_mutex_destroy(&runtime->mutex);
    pthread_mutex_destroy(&runtime->ip_mutex);
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
    status = android_runtime_replace_config(runtime, config_path, false, error);
    if (status == CH_OK) status = android_runtime_start_ip_stack(runtime,
                                                                error);
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
    android_runtime_stop_ip_stack(runtime);
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
    bool running = runtime->running;
    status = android_runtime_replace_config(runtime, config_path, running,
                                            error);
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
    ch_status status;
    if (!runtime->running) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "runtime is not running");
        status = CH_ERROR_INVALID_STATE;
    } else if (runtime->ip_stack == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "Android packet stack is not active");
        status = CH_ERROR_INVALID_STATE;
    } else {
        pthread_mutex_lock(&runtime->ip_mutex);
        status = ch_ip_stack_inject(runtime->ip_stack, packet, length, error);
        pthread_mutex_unlock(&runtime->ip_mutex);
    }
    pthread_mutex_unlock(&runtime->mutex);
    return status;
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
    else if (strcmp(operation, "crypto_self_test") == 0) {
        response = android_runtime_crypto_self_test_json(error);
    }
    else if (strcmp(operation, "servers") == 0 ||
             strcmp(operation, "rules") == 0 ||
             strcmp(operation, "policy_groups") == 0 ||
             strcmp(operation, "rule_sets") == 0 ||
             strcmp(operation, "config") == 0) {
        response = ch_config_query_payload_json(
            runtime->config, runtime->active_profile, operation,
            request_json, error);
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
    if (strcmp(operation, "connect") == 0) {
        if (!runtime->running) {
            ch_status status = android_runtime_start_ip_stack(runtime, error);
            if (status != CH_OK) {
                pthread_mutex_unlock(&runtime->mutex);
                return status;
            }
            runtime->running = true;
        }
    }
    else if (strcmp(operation, "disconnect") == 0) {
        runtime->running = false;
        android_runtime_stop_ip_stack(runtime);
    }
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
        ch_status status = android_runtime_select_profile(runtime, name,
                                                          error);
        if (status != CH_OK) {
            pthread_mutex_unlock(&runtime->mutex);
            return status;
        }
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
