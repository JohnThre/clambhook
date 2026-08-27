#include "clambhook/prompt.h"

#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "internal.h"

#define CH_PROMPT_DEFAULT_TIMEOUT_SECONDS 30
#define CH_PROMPT_MAX_TIMEOUT_SECONDS 3600
#define CH_PROMPT_SILENT_LIMIT 256U

typedef struct prompt_pending {
    ch_prompt_snapshot snapshot;
    char *key;
    pthread_cond_t condition;
    bool resolved;
    bool allow;
    struct prompt_pending *next;
} prompt_pending;

typedef struct prompt_silent {
    ch_prompt_snapshot snapshot;
    bool allow;
    struct prompt_silent *next;
} prompt_silent;

struct ch_prompt_manager {
    pthread_mutex_t mutex;
    pthread_cond_t drained;
    bool enabled;
    bool default_allow;
    int timeout_seconds;
    char silent_mode[6];
    uint64_t sequence;
    prompt_pending *pending;
    prompt_silent *silent;
    size_t silent_count;
};

static int64_t prompt_now_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (int64_t)now.tv_sec * INT64_C(1000000000) + now.tv_nsec;
}

static int prompt_copy(char **out, const char *value) {
    *out = ch_strdup(value == NULL ? "" : value);
    return *out != NULL;
}

void ch_prompt_snapshot_clear(ch_prompt_snapshot *snapshot) {
    if (snapshot == NULL) return;
    free(snapshot->id);
    free(snapshot->conn_id);
    free(snapshot->profile);
    free(snapshot->network);
    free(snapshot->target);
    free(snapshot->target_host);
    free(snapshot->target_port);
    free(snapshot->process_name);
    free(snapshot->process_path);
    free(snapshot->code_sign_id);
    free(snapshot->code_sign_status);
    free(snapshot->would_use_chain);
    free(snapshot->would_use_group);
    memset(snapshot, 0, sizeof(*snapshot));
}

void ch_prompt_action_options_clear(ch_prompt_action_options *options) {
    if (options == NULL) return;
    free(options->id);
    free(options->scope);
    memset(options, 0, sizeof(*options));
}

ch_status ch_prompt_action_options_parse(const char *request_json,
                                         bool require_action,
                                         ch_prompt_action_options *options,
                                         ch_error *error) {
    ch_error_clear(error);
    if (request_json == NULL || options == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt request and options are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(options, 0, sizeof(*options));
    ch_json_value *request = ch_json_parse(
        request_json, strlen(request_json), error);
    if (request == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    const char *id = ch_json_string_value(ch_json_object_get(request, "id"));
    const char *scope = ch_json_string_value(ch_json_object_get(request,
                                                                 "scope"));
    const char *action = ch_json_string_value(ch_json_object_get(request,
                                                                  "action"));
    int parsed_action = action != NULL && strcasecmp(action, "allow") == 0 ?
        1 : action != NULL && strcasecmp(action, "block") == 0 ? 0 : -1;
    options->id = ch_strdup(id == NULL ? "" : id);
    options->scope = ch_strdup(scope == NULL || scope[0] == '\0' ?
                               (require_action ? "once" : "session") :
                               scope);
    options->match_host = ch_json_bool_value(
        ch_json_object_get(request, "match_host"), false);
    options->match_port = ch_json_bool_value(
        ch_json_object_get(request, "match_port"), false);
    options->match_protocol = ch_json_bool_value(
        ch_json_object_get(request, "match_protocol"), false);
    double ttl = ch_json_number_value(ch_json_object_get(request,
                                                          "ttl_seconds"),
                                      0.0);
    ch_json_value_destroy(request);
    if (options->id == NULL || options->scope == NULL) {
        ch_prompt_action_options_clear(options);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy prompt request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (options->id[0] == '\0') {
        ch_prompt_action_options_clear(options);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt identifier is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (ttl < 0.0 || ttl > 86400.0 || ttl != (double)(long long)ttl) {
        ch_prompt_action_options_clear(options);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "ttl_seconds must be an integer from 0 to 86400");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    options->ttl_seconds = (long long)ttl;
    if (strcasecmp(options->scope, "once") != 0 &&
        strcasecmp(options->scope, "session") != 0 &&
        strcasecmp(options->scope, "until_quit") != 0 &&
        strcasecmp(options->scope, "forever") != 0) {
        ch_prompt_action_options_clear(options);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "scope must be once, session, until_quit, or forever");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (require_action) {
        if (parsed_action == 1) {
            options->allow = true;
        } else if (parsed_action != 0) {
            ch_prompt_action_options_clear(options);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "action must be allow or block");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static int prompt_snapshot_copy(ch_prompt_snapshot *out,
                                const ch_prompt_snapshot *source) {
    memset(out, 0, sizeof(*out));
    if (!prompt_copy(&out->id, source->id) ||
        !prompt_copy(&out->conn_id, source->conn_id) ||
        !prompt_copy(&out->profile, source->profile) ||
        !prompt_copy(&out->network, source->network) ||
        !prompt_copy(&out->target, source->target) ||
        !prompt_copy(&out->target_host, source->target_host) ||
        !prompt_copy(&out->target_port, source->target_port) ||
        !prompt_copy(&out->process_name, source->process_name) ||
        !prompt_copy(&out->process_path, source->process_path) ||
        !prompt_copy(&out->code_sign_id, source->code_sign_id) ||
        !prompt_copy(&out->code_sign_status, source->code_sign_status) ||
        !prompt_copy(&out->would_use_chain, source->would_use_chain) ||
        !prompt_copy(&out->would_use_group, source->would_use_group)) {
        ch_prompt_snapshot_clear(out);
        return 0;
    }
    out->process_pid = source->process_pid;
    out->created_ns = source->created_ns;
    out->expires_ns = source->expires_ns;
    out->waiters = source->waiters;
    return 1;
}

static int prompt_snapshot_from_request(ch_prompt_snapshot *out,
                                        const ch_prompt_request *request,
                                        const char *id, int64_t now,
                                        int timeout_seconds) {
    memset(out, 0, sizeof(*out));
    if (!prompt_copy(&out->id, id) ||
        !prompt_copy(&out->conn_id, request->conn_id) ||
        !prompt_copy(&out->profile, request->profile) ||
        !prompt_copy(&out->network, request->network) ||
        !prompt_copy(&out->target, request->target) ||
        !prompt_copy(&out->target_host, request->target_host) ||
        !prompt_copy(&out->target_port, request->target_port) ||
        !prompt_copy(&out->process_name, request->process_name) ||
        !prompt_copy(&out->process_path, request->process_path) ||
        !prompt_copy(&out->code_sign_id, request->code_sign_id) ||
        !prompt_copy(&out->code_sign_status, request->code_sign_status) ||
        !prompt_copy(&out->would_use_chain, request->would_use_chain) ||
        !prompt_copy(&out->would_use_group, request->would_use_group)) {
        ch_prompt_snapshot_clear(out);
        return 0;
    }
    out->process_pid = request->process_pid;
    out->created_ns = now;
    out->expires_ns = now + (int64_t)timeout_seconds * INT64_C(1000000000);
    return 1;
}

static char *prompt_key(const ch_prompt_request *request) {
    const char *process = request->process_path;
    char fallback[64];
    if (process == NULL || process[0] == '\0') process = request->process_name;
    if (process == NULL || process[0] == '\0') {
        (void)snprintf(fallback, sizeof(fallback), "pid:%d|source:%s",
                       request->process_pid,
                       request->source == NULL ? "" : request->source);
        process = fallback;
    }
    const char *parts[] = {
        request->profile, process, request->network, request->target_host,
        request->target_port
    };
    size_t length = 1U;
    for (size_t index = 0U; index < 5U; ++index) {
        length += strlen(parts[index] == NULL ? "" : parts[index]) + 1U;
    }
    char *key = malloc(length);
    if (key == NULL) return NULL;
    (void)snprintf(key, length, "%s|%s|%s|%s|%s",
                   parts[0] == NULL ? "" : parts[0], process,
                   parts[2] == NULL ? "" : parts[2],
                   parts[3] == NULL ? "" : parts[3],
                   parts[4] == NULL ? "" : parts[4]);
    return key;
}

static void prompt_pending_free(prompt_pending *pending) {
    if (pending == NULL) return;
    pthread_cond_destroy(&pending->condition);
    ch_prompt_snapshot_clear(&pending->snapshot);
    free(pending->key);
    free(pending);
}

static void prompt_silent_free(prompt_silent *silent) {
    if (silent == NULL) return;
    ch_prompt_snapshot_clear(&silent->snapshot);
    free(silent);
}

ch_prompt_manager *ch_prompt_manager_create(ch_error *error) {
    ch_error_clear(error);
    ch_prompt_manager *manager = calloc(1U, sizeof(*manager));
    if (manager == NULL || pthread_mutex_init(&manager->mutex, NULL) != 0) {
        free(manager);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize prompt manager");
        return NULL;
    }
    if (pthread_cond_init(&manager->drained, NULL) != 0) {
        pthread_mutex_destroy(&manager->mutex);
        free(manager);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize prompt waiters");
        return NULL;
    }
    manager->timeout_seconds = CH_PROMPT_DEFAULT_TIMEOUT_SECONDS;
    return manager;
}

static void prompt_resolve_all_locked(ch_prompt_manager *manager, bool allow) {
    for (prompt_pending *pending = manager->pending; pending != NULL;
         pending = pending->next) {
        if (pending->resolved) continue;
        pending->resolved = true;
        pending->allow = allow;
        pthread_cond_broadcast(&pending->condition);
    }
}

void ch_prompt_manager_cancel_all(ch_prompt_manager *manager) {
    if (manager == NULL) return;
    pthread_mutex_lock(&manager->mutex);
    prompt_resolve_all_locked(manager, false);
    pthread_mutex_unlock(&manager->mutex);
}

void ch_prompt_manager_destroy(ch_prompt_manager *manager) {
    if (manager == NULL) return;
    pthread_mutex_lock(&manager->mutex);
    manager->enabled = false;
    prompt_resolve_all_locked(manager, false);
    for (;;) {
        bool waiting = false;
        for (prompt_pending *pending = manager->pending; pending != NULL;
             pending = pending->next) {
            if (pending->snapshot.waiters > 0U) {
                waiting = true;
                break;
            }
        }
        if (!waiting) break;
        pthread_cond_wait(&manager->drained, &manager->mutex);
    }
    prompt_pending *pending = manager->pending;
    while (pending != NULL) {
        prompt_pending *next = pending->next;
        prompt_pending_free(pending);
        pending = next;
    }
    prompt_silent *silent = manager->silent;
    while (silent != NULL) {
        prompt_silent *next = silent->next;
        prompt_silent_free(silent);
        silent = next;
    }
    pthread_mutex_unlock(&manager->mutex);
    pthread_cond_destroy(&manager->drained);
    pthread_mutex_destroy(&manager->mutex);
    free(manager);
}

ch_status ch_prompt_manager_configure(ch_prompt_manager *manager,
                                      const ch_config *config,
                                      ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt manager is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    bool enabled = false;
    bool default_allow = false;
    int timeout_seconds = CH_PROMPT_DEFAULT_TIMEOUT_SECONDS;
    char *silent_mode = ch_strdup("");
    const ch_config_table *prompt = config == NULL ? NULL :
        ch_config_table_get_table(ch_config_root(config), "prompt");
    ch_error field_error;
    if (silent_mode == NULL) goto out_of_memory;
    if (prompt != NULL && ch_config_table_has(prompt, "enabled") &&
        ch_config_table_get_bool(prompt, "enabled", &enabled,
                                 &field_error) != CH_OK) goto field_failure;
    if (prompt != NULL && ch_config_table_has(prompt, "default_allow") &&
        ch_config_table_get_bool(prompt, "default_allow", &default_allow,
                                 &field_error) != CH_OK) goto field_failure;
    if (prompt != NULL && ch_config_table_has(prompt, "timeout_seconds")) {
        int64_t timeout = 0;
        if (ch_config_table_get_int(prompt, "timeout_seconds", &timeout,
                                    &field_error) != CH_OK) goto field_failure;
        if (timeout > 0) timeout_seconds = timeout > CH_PROMPT_MAX_TIMEOUT_SECONDS ?
            CH_PROMPT_MAX_TIMEOUT_SECONDS : (int)timeout;
    }
    if (prompt != NULL && ch_config_table_has(prompt, "silent_mode")) {
        free(silent_mode);
        silent_mode = NULL;
        if (ch_config_table_get_string(prompt, "silent_mode", &silent_mode,
                                       &field_error) != CH_OK) goto field_failure;
    }
    pthread_mutex_lock(&manager->mutex);
    prompt_resolve_all_locked(manager, false);
    manager->enabled = enabled;
    manager->default_allow = default_allow;
    manager->timeout_seconds = timeout_seconds;
    (void)snprintf(manager->silent_mode, sizeof(manager->silent_mode), "%s",
                   silent_mode);
    pthread_mutex_unlock(&manager->mutex);
    free(silent_mode);
    return CH_OK;

field_failure:
    free(silent_mode);
    if (error != NULL) *error = field_error;
    return field_error.code;
out_of_memory:
    free(silent_mode);
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                 "configure prompt manager");
    return CH_ERROR_OUT_OF_MEMORY;
}

bool ch_prompt_manager_enabled(ch_prompt_manager *manager) {
    if (manager == NULL) return false;
    pthread_mutex_lock(&manager->mutex);
    bool enabled = manager->enabled;
    pthread_mutex_unlock(&manager->mutex);
    return enabled;
}

static prompt_pending **prompt_pending_link(ch_prompt_manager *manager,
                                            prompt_pending *target) {
    prompt_pending **link = &manager->pending;
    while (*link != NULL && *link != target) link = &(*link)->next;
    return link;
}

static prompt_pending *prompt_pending_key(ch_prompt_manager *manager,
                                          const char *key) {
    for (prompt_pending *pending = manager->pending; pending != NULL;
         pending = pending->next) {
        if (!pending->resolved && strcmp(pending->key, key) == 0) return pending;
    }
    return NULL;
}

static void prompt_record_silent_locked(ch_prompt_manager *manager,
                                        const ch_prompt_request *request,
                                        bool allow) {
    prompt_silent *silent = calloc(1U, sizeof(*silent));
    if (silent == NULL) return;
    char identifier[64];
    (void)snprintf(identifier, sizeof(identifier), "silent-%" PRIu64,
                   ++manager->sequence);
    int64_t now = prompt_now_ns();
    if (!prompt_snapshot_from_request(&silent->snapshot, request, identifier,
                                      now, 0)) {
        free(silent);
        return;
    }
    silent->snapshot.expires_ns = 0;
    silent->allow = allow;
    silent->next = manager->silent;
    manager->silent = silent;
    ++manager->silent_count;
    if (manager->silent_count <= CH_PROMPT_SILENT_LIMIT) return;
    prompt_silent *cursor = manager->silent;
    while (cursor->next != NULL && cursor->next->next != NULL) {
        cursor = cursor->next;
    }
    prompt_silent_free(cursor->next);
    cursor->next = NULL;
    --manager->silent_count;
}

ch_status ch_prompt_manager_await(ch_prompt_manager *manager,
                                  const ch_prompt_request *request,
                                  bool *out_allow, bool *out_gated,
                                  ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || request == NULL || out_allow == NULL ||
        out_gated == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt await inputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_allow = false;
    *out_gated = false;
    pthread_mutex_lock(&manager->mutex);
    if (!manager->enabled) {
        pthread_mutex_unlock(&manager->mutex);
        return CH_OK;
    }
    *out_gated = true;
    if (manager->silent_mode[0] != '\0') {
        *out_allow = strcmp(manager->silent_mode, "allow") == 0;
        prompt_record_silent_locked(manager, request, *out_allow);
        pthread_mutex_unlock(&manager->mutex);
        return CH_OK;
    }
    char *key = prompt_key(request);
    if (key == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate prompt key");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    prompt_pending *pending = prompt_pending_key(manager, key);
    if (pending == NULL) {
        pending = calloc(1U, sizeof(*pending));
        char identifier[64];
        (void)snprintf(identifier, sizeof(identifier), "prompt-%" PRIu64,
                       ++manager->sequence);
        int64_t now = prompt_now_ns();
        if (pending == NULL) {
            free(key);
            pthread_mutex_unlock(&manager->mutex);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate pending prompt");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (pthread_cond_init(&pending->condition, NULL) != 0) {
            free(pending);
            free(key);
            pthread_mutex_unlock(&manager->mutex);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "initialize pending prompt");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (!prompt_snapshot_from_request(
                &pending->snapshot, request, identifier, now,
                manager->timeout_seconds)) {
            prompt_pending_free(pending);
            free(key);
            pthread_mutex_unlock(&manager->mutex);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy pending prompt");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        pending->key = key;
        key = NULL;
        pending->next = manager->pending;
        manager->pending = pending;
    }
    free(key);
    ++pending->snapshot.waiters;
    struct timespec deadline = {
        .tv_sec = (time_t)(pending->snapshot.expires_ns /
                          INT64_C(1000000000)),
        .tv_nsec = (long)(pending->snapshot.expires_ns %
                         INT64_C(1000000000))
    };
    while (!pending->resolved) {
        int wait_status = pthread_cond_timedwait(
            &pending->condition, &manager->mutex, &deadline);
        if (wait_status == ETIMEDOUT && !pending->resolved) {
            pending->resolved = true;
            pending->allow = manager->default_allow;
            pthread_cond_broadcast(&pending->condition);
        } else if (wait_status != 0 && wait_status != EINTR) {
            pending->resolved = true;
            pending->allow = false;
            pthread_cond_broadcast(&pending->condition);
        }
    }
    *out_allow = pending->allow;
    --pending->snapshot.waiters;
    if (pending->snapshot.waiters == 0U) {
        prompt_pending **link = prompt_pending_link(manager, pending);
        if (*link == pending) *link = pending->next;
        prompt_pending_free(pending);
        pthread_cond_broadcast(&manager->drained);
    }
    pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

static int prompt_append_time(ch_json_buffer *json, int64_t timestamp_ns) {
    time_t seconds = (time_t)(timestamp_ns / INT64_C(1000000000));
    long nanoseconds = (long)(timestamp_ns % INT64_C(1000000000));
    struct tm utc;
    if (gmtime_r(&seconds, &utc) == NULL) return ch_json_append_string(json, "");
    char value[48];
    int length = snprintf(
        value, sizeof(value), "%04d-%02d-%02dT%02d:%02d:%02d.%09ldZ",
        utc.tm_year + 1900, utc.tm_mon + 1, utc.tm_mday, utc.tm_hour,
        utc.tm_min, utc.tm_sec, nanoseconds);
    return length > 0 && (size_t)length < sizeof(value) &&
        ch_json_append_string(json, value);
}

static int prompt_append_snapshot(ch_json_buffer *json,
                                  const ch_prompt_snapshot *snapshot) {
    return ch_json_append(json, "{\"id\":") &&
        ch_json_append_string(json, snapshot->id) &&
        ch_json_append(json, ",\"conn_id\":") &&
        ch_json_append_string(json, snapshot->conn_id) &&
        ch_json_append(json, ",\"profile\":") &&
        ch_json_append_string(json, snapshot->profile) &&
        ch_json_append(json, ",\"network\":") &&
        ch_json_append_string(json, snapshot->network) &&
        ch_json_append(json, ",\"target\":") &&
        ch_json_append_string(json, snapshot->target) &&
        ch_json_append(json, ",\"target_host\":") &&
        ch_json_append_string(json, snapshot->target_host) &&
        ch_json_append(json, ",\"target_port\":") &&
        ch_json_append_string(json, snapshot->target_port) &&
        ch_json_append_format(json, ",\"pid\":%d,\"process_name\":",
                              snapshot->process_pid) &&
        ch_json_append_string(json, snapshot->process_name) &&
        ch_json_append(json, ",\"process_path\":") &&
        ch_json_append_string(json, snapshot->process_path) &&
        ch_json_append(json, ",\"created_at\":") &&
        prompt_append_time(json, snapshot->created_ns) &&
        ch_json_append(json, ",\"expires_at\":") &&
        prompt_append_time(json, snapshot->expires_ns) &&
        ch_json_append_format(json, ",\"waiters\":%zu,\"would_use_chain\":",
                              snapshot->waiters) &&
        ch_json_append_string(json, snapshot->would_use_chain) &&
        ch_json_append(json, ",\"would_use_group\":") &&
        ch_json_append_string(json, snapshot->would_use_group) &&
        ch_json_append(json, ",\"code_sign_id\":") &&
        ch_json_append_string(json, snapshot->code_sign_id) &&
        ch_json_append(json, ",\"code_sign_status\":") &&
        ch_json_append_string(json, snapshot->code_sign_status) &&
        ch_json_append(json, "}");
}

char *ch_prompt_manager_pending_json(ch_prompt_manager *manager,
                                     ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) return ch_strdup("{\"prompts\":[]}");
    pthread_mutex_lock(&manager->mutex);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"prompts\":[");
    size_t written = 0U;
    for (prompt_pending *pending = manager->pending;
         okay && pending != NULL; pending = pending->next) {
        if (pending->resolved) continue;
        if (written > 0U) okay = ch_json_append(&json, ",");
        if (okay) okay = prompt_append_snapshot(&json, &pending->snapshot);
        ++written;
    }
    if (okay) okay = ch_json_append(&json, "]}");
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode pending prompts");
    }
    return result;
}

char *ch_prompt_manager_silent_json(ch_prompt_manager *manager,
                                    ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) return ch_strdup("{\"decisions\":[]}");
    pthread_mutex_lock(&manager->mutex);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"decisions\":[");
    size_t written = 0U;
    for (prompt_silent *silent = manager->silent;
         okay && silent != NULL; silent = silent->next) {
        const ch_prompt_snapshot *snapshot = &silent->snapshot;
        if (written > 0U) okay = ch_json_append(&json, ",");
        if (okay) okay = ch_json_append(&json, "{\"id\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->id);
        if (okay) okay = ch_json_append(&json, ",\"profile\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->profile);
        if (okay) okay = ch_json_append(&json, ",\"network\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->network);
        if (okay) okay = ch_json_append(&json, ",\"target\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->target);
        if (okay) okay = ch_json_append(&json, ",\"target_host\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->target_host);
        if (okay) okay = ch_json_append(&json, ",\"target_port\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->target_port);
        if (okay) okay = ch_json_append_format(
            &json, ",\"pid\":%d,\"process_name\":", snapshot->process_pid);
        if (okay) okay = ch_json_append_string(&json, snapshot->process_name);
        if (okay) okay = ch_json_append(&json, ",\"process_path\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->process_path);
        if (okay) okay = ch_json_append(&json, ",\"code_sign_id\":");
        if (okay) okay = ch_json_append_string(&json, snapshot->code_sign_id);
        if (okay) okay = ch_json_append(&json, ",\"action\":");
        if (okay) okay = ch_json_append_string(
            &json, silent->allow ? "allow" : "deny");
        if (okay) okay = ch_json_append_format(
            &json, ",\"ts_ns\":%" PRId64 "}", snapshot->created_ns);
        ++written;
    }
    if (okay) okay = ch_json_append(&json, "]}");
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode silent prompt decisions");
    }
    return result;
}

ch_status ch_prompt_manager_resolve(ch_prompt_manager *manager,
                                    const char *id, bool allow,
                                    ch_prompt_snapshot *out_snapshot,
                                    ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || id == NULL || id[0] == '\0' ||
        out_snapshot == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt identifier and snapshot are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_snapshot, 0, sizeof(*out_snapshot));
    pthread_mutex_lock(&manager->mutex);
    prompt_pending *pending = manager->pending;
    while (pending != NULL &&
           (pending->resolved || strcmp(pending->snapshot.id, id) != 0)) {
        pending = pending->next;
    }
    if (pending == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "prompt not found");
        return CH_ERROR_NOT_FOUND;
    }
    if (!prompt_snapshot_copy(out_snapshot, &pending->snapshot)) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy resolved prompt");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pending->resolved = true;
    pending->allow = allow;
    pthread_cond_broadcast(&pending->condition);
    pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

ch_status ch_prompt_manager_silent_decision(ch_prompt_manager *manager,
                                            const char *id,
                                            ch_prompt_snapshot *out_snapshot,
                                            bool *out_allow,
                                            ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || id == NULL || out_snapshot == NULL ||
        out_allow == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "silent decision inputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_snapshot, 0, sizeof(*out_snapshot));
    pthread_mutex_lock(&manager->mutex);
    prompt_silent *silent = manager->silent;
    while (silent != NULL && strcmp(silent->snapshot.id, id) != 0) {
        silent = silent->next;
    }
    if (silent == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "silent decision not found");
        return CH_ERROR_NOT_FOUND;
    }
    if (!prompt_snapshot_copy(out_snapshot, &silent->snapshot)) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy silent decision");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *out_allow = silent->allow;
    pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

char *ch_prompt_rule_request_json(const ch_prompt_snapshot *snapshot,
                                  const ch_config *config, bool allow,
                                  bool match_host, bool match_port,
                                  bool match_protocol, ch_error *error) {
    ch_error_clear(error);
    if (snapshot == NULL || config == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt snapshot and config are required");
        return NULL;
    }
    const char *process = snapshot->process_path[0] != '\0' ?
        snapshot->process_path : snapshot->process_name;
    if (process[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "prompt has no attributed process to remember");
        return NULL;
    }
    const char *action = "block";
    char *chain_action = NULL;
    if (allow) {
        action = "direct";
        const ch_config_table *profile = ch_config_profile_named(
            config, snapshot->profile);
        const ch_config_array *chains = ch_config_table_get_array(profile,
                                                                   "chain");
        const ch_config_table *first = ch_config_array_get_table(chains, 0U);
        char *chain_name = NULL;
        ch_error ignored;
        if (first != NULL && ch_config_table_get_string(
                first, "name", &chain_name, &ignored) == CH_OK) {
            size_t length = strlen(chain_name) + sizeof("chain:");
            chain_action = malloc(length);
            if (chain_action != NULL) {
                (void)snprintf(chain_action, length, "chain:%s", chain_name);
                action = chain_action;
            }
        }
        free(chain_name);
    }
    const char *label = snapshot->process_name[0] != '\0' ?
        snapshot->process_name : process;
    ch_json_buffer name;
    ch_json_init(&name);
    int okay = ch_json_append_format(&name, "prompt %s %s",
                                     allow ? "allow" : "block", label);
    if (okay && match_host && snapshot->target_host[0] != '\0') {
        okay = ch_json_append(&name, " ") &&
            ch_json_append(&name, snapshot->target_host);
    }
    char *rule_name = okay ? ch_json_take(&name) : NULL;
    ch_json_dispose(&name);
    ch_json_buffer json;
    ch_json_init(&json);
    okay = rule_name != NULL && ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, snapshot->profile) &&
        ch_json_append(&json, ",\"rule\":{\"name\":") &&
        ch_json_append_string(&json, rule_name) &&
        ch_json_append(&json, ",\"action\":") &&
        ch_json_append_string(&json, action) &&
        ch_json_append(&json, ",\"processes\":[") &&
        ch_json_append_string(&json, process) && ch_json_append(&json, "]");
    if (okay && match_host && snapshot->target_host[0] != '\0') {
        okay = ch_json_append(&json, ",\"domains\":[") &&
            ch_json_append_string(&json, snapshot->target_host) &&
            ch_json_append(&json, "]");
    }
    if (okay && match_port && snapshot->target_port[0] != '\0') {
        char *end = NULL;
        long port = strtol(snapshot->target_port, &end, 10);
        if (end != snapshot->target_port && *end == '\0' && port > 0 &&
            port <= 65535) {
            okay = ch_json_append_format(&json, ",\"ports\":[%ld]", port);
        }
    }
    if (okay && match_protocol && snapshot->network[0] != '\0') {
        okay = ch_json_append(&json, ",\"networks\":[") &&
            ch_json_append_string(&json, snapshot->network) &&
            ch_json_append(&json, "]");
    }
    if (okay) okay = ch_json_append_format(
        &json, "},\"position\":\"%s\"}", allow ? "append" : "prepend");
    free(rule_name);
    free(chain_action);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode remembered prompt rule");
    }
    return result;
}
