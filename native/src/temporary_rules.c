// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/temporary_rules.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/traffic.h"
#include "internal.h"

#define CH_TEMPORARY_RULE_DEFAULT_LIMIT 128U
#define CH_TEMPORARY_RULE_MAX_LIMIT 1024U
#define CH_TEMPORARY_RULE_DEFAULT_TTL 900
#define CH_TEMPORARY_RULE_MAX_TTL 86400

typedef enum temporary_match_kind {
    TEMPORARY_MATCH_NONE,
    TEMPORARY_MATCH_DOMAIN,
    TEMPORARY_MATCH_SUFFIX,
    TEMPORARY_MATCH_CIDR
} temporary_match_kind;

typedef struct temporary_rule {
    char *id;
    char *profile;
    char *name;
    char *action;
    temporary_match_kind match_kind;
    char *match_value;
    char *process;
    char *network;
    char *port;
    int until_quit_pid;
    int64_t created_ns;
    int64_t expires_ns;
    char *source_conn_id;
    char *source_target;
    char *source_target_host;
} temporary_rule;

struct ch_temporary_rules {
    pthread_mutex_t mutex;
    temporary_rule *items;
    size_t count;
    size_t limit;
    uint64_t sequence;
};

static int64_t temporary_now_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (int64_t)now.tv_sec * INT64_C(1000000000) +
        (int64_t)now.tv_nsec;
}

static uint64_t temporary_monotonic_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) return 0U;
    return (uint64_t)now.tv_sec * UINT64_C(1000000000) +
        (uint64_t)now.tv_nsec;
}

static int temporary_copy(char **out, const char *value) {
    *out = ch_strdup(value == NULL ? "" : value);
    return *out != NULL;
}

static void temporary_rule_clear(temporary_rule *rule) {
    if (rule == NULL) return;
    free(rule->id);
    free(rule->profile);
    free(rule->name);
    free(rule->action);
    free(rule->match_value);
    free(rule->process);
    free(rule->network);
    free(rule->port);
    free(rule->source_conn_id);
    free(rule->source_target);
    free(rule->source_target_host);
    memset(rule, 0, sizeof(*rule));
}

static void temporary_prune_locked(ch_temporary_rules *rules, int64_t now) {
    size_t write = 0U;
    for (size_t index = 0U; index < rules->count; ++index) {
        int owner_exited = rules->items[index].until_quit_pid > 0 &&
            kill(rules->items[index].until_quit_pid, 0) != 0 &&
            errno == ESRCH;
        if (rules->items[index].expires_ns <= now || owner_exited) {
            temporary_rule_clear(&rules->items[index]);
            continue;
        }
        if (write != index) rules->items[write] = rules->items[index];
        ++write;
    }
    if (write < rules->count) {
        memset(rules->items + write, 0,
               (rules->count - write) * sizeof(*rules->items));
    }
    rules->count = write;
}

ch_temporary_rules *ch_temporary_rules_create(size_t limit, ch_error *error) {
    ch_error_clear(error);
    if (limit == 0U) limit = CH_TEMPORARY_RULE_DEFAULT_LIMIT;
    if (limit > CH_TEMPORARY_RULE_MAX_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule limit exceeds %u",
                     (unsigned int)CH_TEMPORARY_RULE_MAX_LIMIT);
        return NULL;
    }
    ch_temporary_rules *rules = calloc(1U, sizeof(*rules));
    if (rules == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate temporary rules");
        return NULL;
    }
    rules->items = calloc(limit, sizeof(*rules->items));
    if (rules->items == NULL || pthread_mutex_init(&rules->mutex, NULL) != 0) {
        free(rules->items);
        free(rules);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize temporary rules");
        return NULL;
    }
    rules->limit = limit;
    return rules;
}

void ch_temporary_rules_destroy(ch_temporary_rules *rules) {
    if (rules == NULL) return;
    pthread_mutex_lock(&rules->mutex);
    for (size_t index = 0U; index < rules->count; ++index) {
        temporary_rule_clear(&rules->items[index]);
    }
    free(rules->items);
    pthread_mutex_unlock(&rules->mutex);
    pthread_mutex_destroy(&rules->mutex);
    free(rules);
}

static char *temporary_request_string(const ch_json_value *request,
                                      const char *key) {
    const char *value = ch_json_string_value(ch_json_object_get(request, key));
    return ch_strdup(value == NULL ? "" : value);
}

static char *temporary_first_string(const ch_json_value *object,
                                    const char *key) {
    const ch_json_value *array = ch_json_object_get(object, key);
    const char *value = ch_json_string_value(ch_json_array_get(array, 0U));
    return ch_strdup(value == NULL ? "" : value);
}

static char *temporary_first_port(const ch_json_value *object) {
    const ch_json_value *array = ch_json_object_get(object, "ports");
    const ch_json_value *value = ch_json_array_get(array, 0U);
    if (value == NULL) return ch_strdup("");
    double number = ch_json_number_value(value, -1.0);
    if (number < 1.0 || number > 65535.0 || number != (double)(long)number) {
        return NULL;
    }
    char port[16];
    (void)snprintf(port, sizeof(port), "%ld", (long)number);
    return ch_strdup(port);
}

static ch_status temporary_rule_from_request(
    const char *mutation_json, const char *original_request,
    ch_traffic_store *traffic, temporary_rule *out, long long *ttl_seconds,
    ch_error *error) {
    memset(out, 0, sizeof(*out));
    ch_json_value *mutation = ch_json_parse(
        mutation_json, strlen(mutation_json), error);
    ch_json_value *request = ch_json_parse(
        original_request, strlen(original_request), error);
    if (mutation == NULL || request == NULL) {
        ch_json_value_destroy(mutation);
        ch_json_value_destroy(request);
        return error == NULL ? CH_ERROR_PARSE : error->code;
    }
    const ch_json_value *rule = ch_json_object_get(mutation, "rule");
    const ch_json_value *match = NULL;
    if (ch_json_value_type(rule) != CH_JSON_OBJECT) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "connection rule response has no rule");
        goto failure;
    }
    out->profile = temporary_request_string(mutation, "profile");
    out->name = temporary_request_string(rule, "name");
    out->action = temporary_request_string(rule, "action");
    out->process = temporary_first_string(rule, "processes");
    out->network = temporary_first_string(rule, "networks");
    out->port = temporary_first_port(rule);
    const ch_json_value *array = ch_json_object_get(rule, "domains");
    if (ch_json_array_size(array) > 0U) {
        out->match_kind = TEMPORARY_MATCH_DOMAIN;
        match = ch_json_array_get(array, 0U);
    } else {
        array = ch_json_object_get(rule, "domain_suffixes");
        if (ch_json_array_size(array) > 0U) {
            out->match_kind = TEMPORARY_MATCH_SUFFIX;
            match = ch_json_array_get(array, 0U);
        } else {
            array = ch_json_object_get(rule, "cidrs");
            if (ch_json_array_size(array) > 0U) {
                out->match_kind = TEMPORARY_MATCH_CIDR;
                match = ch_json_array_get(array, 0U);
            }
        }
    }
    const char *match_string = ch_json_string_value(match);
    out->match_value = ch_strdup(match_string == NULL ? "" : match_string);
    out->source_conn_id = temporary_request_string(request, "conn_id");
    if (traffic != NULL) {
        ch_traffic_connection connection;
        if (out->source_conn_id == NULL || out->source_conn_id[0] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "connection identifier is required");
            goto failure;
        }
        ch_status connection_status = ch_traffic_connection_copy(
            traffic, out->source_conn_id, &connection, error);
        if (connection_status != CH_OK) goto failure;
        out->source_target = ch_strdup(connection.target);
        out->source_target_host = ch_strdup(connection.target_host);
        ch_traffic_connection_clear(&connection);
    } else {
        out->source_target = temporary_request_string(request,
                                                       "source_target");
        out->source_target_host = temporary_request_string(
            request, "source_target_host");
    }
    double ttl = ch_json_number_value(ch_json_object_get(request,
                                                          "ttl_seconds"),
                                      CH_TEMPORARY_RULE_DEFAULT_TTL);
    *ttl_seconds = ttl <= 0.0 ? CH_TEMPORARY_RULE_DEFAULT_TTL :
        (long long)ttl;
    if (*ttl_seconds > CH_TEMPORARY_RULE_MAX_TTL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "ttl_seconds must be at most 86400");
        goto failure;
    }
    if (out->profile == NULL || out->profile[0] == '\0' ||
        out->name == NULL || out->name[0] == '\0' ||
        out->action == NULL || out->action[0] == '\0' ||
        out->match_value == NULL || out->process == NULL ||
        out->network == NULL || out->port == NULL ||
        (out->match_value[0] == '\0' && out->process[0] == '\0') ||
        out->source_conn_id == NULL || out->source_target == NULL ||
        out->source_target_host == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy temporary rule");
        goto failure;
    }
    ch_json_value_destroy(mutation);
    ch_json_value_destroy(request);
    return CH_OK;

failure:
    ch_json_value_destroy(mutation);
    ch_json_value_destroy(request);
    temporary_rule_clear(out);
    return error == NULL || error->code == CH_OK ? CH_ERROR_PARSE :
                                                   error->code;
}

static int temporary_append_json(ch_json_buffer *json,
                                 const temporary_rule *rule) {
    const char *match_key = rule->match_kind == TEMPORARY_MATCH_DOMAIN ?
        "domains" : rule->match_kind == TEMPORARY_MATCH_SUFFIX ?
        "domain_suffixes" : rule->match_kind == TEMPORARY_MATCH_CIDR ?
        "cidrs" : NULL;
    int okay = ch_json_append(json, "{\"id\":") &&
        ch_json_append_string(json, rule->id) &&
        ch_json_append(json, ",\"profile\":") &&
        ch_json_append_string(json, rule->profile) &&
        ch_json_append(json, ",\"rule\":{\"name\":") &&
        ch_json_append_string(json, rule->name) &&
        ch_json_append(json, ",\"action\":") &&
        ch_json_append_string(json, rule->action);
    if (okay && rule->process[0] != '\0') {
        okay = ch_json_append(json, ",\"processes\":[") &&
            ch_json_append_string(json, rule->process) &&
            ch_json_append(json, "]");
    }
    if (okay && match_key != NULL) {
        okay = ch_json_append(json, ",\"") &&
            ch_json_append(json, match_key) && ch_json_append(json, "\":[") &&
            ch_json_append_string(json, rule->match_value) &&
            ch_json_append(json, "]");
    }
    if (okay && rule->port[0] != '\0') {
        okay = ch_json_append(json, ",\"ports\":[") &&
            ch_json_append(json, rule->port) && ch_json_append(json, "]");
    }
    if (okay && rule->network[0] != '\0') {
        okay = ch_json_append(json, ",\"networks\":[") &&
            ch_json_append_string(json, rule->network) &&
            ch_json_append(json, "]");
    }
    return okay && ch_json_append_format(json,
            "},\"created_ts_ns\":%" PRId64
            ",\"expires_ts_ns\":%" PRId64 ",\"source_conn_id\":",
            rule->created_ns, rule->expires_ns) &&
        ch_json_append_string(json, rule->source_conn_id) &&
        ch_json_append(json, ",\"source_target\":") &&
        ch_json_append_string(json, rule->source_target) &&
        ch_json_append(json, ",\"source_target_host\":") &&
        ch_json_append_string(json, rule->source_target_host) &&
        ch_json_append_format(json, ",\"until_quit_pid\":%d",
                              rule->until_quit_pid) &&
        ch_json_append(json, "}");
}

static int temporary_append_snapshot_locked(ch_json_buffer *json,
                                            ch_temporary_rules *rules,
                                            const char *profile) {
    if (!ch_json_append(json, "[")) return 0;
    size_t written = 0U;
    for (size_t index = 0U; index < rules->count; ++index) {
        temporary_rule *rule = &rules->items[index];
        if (profile != NULL && profile[0] != '\0' &&
            strcmp(rule->profile, profile) != 0) continue;
        if (written > 0U && !ch_json_append(json, ",")) return 0;
        if (!temporary_append_json(json, rule)) return 0;
        ++written;
    }
    return ch_json_append(json, "]");
}

char *ch_temporary_rules_snapshot_json(ch_temporary_rules *rules,
                                       const char *profile,
                                       ch_error *error) {
    ch_error_clear(error);
    if (rules == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule manager is required");
        return NULL;
    }
    pthread_mutex_lock(&rules->mutex);
    temporary_prune_locked(rules, temporary_now_ns());
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = temporary_append_snapshot_locked(&json, rules, profile);
    pthread_mutex_unlock(&rules->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode temporary rules");
    }
    return result;
}

char *ch_temporary_rules_payload_json(ch_temporary_rules *rules,
                                      const char *profile,
                                      ch_error *error) {
    char *snapshot = ch_temporary_rules_snapshot_json(rules, profile, error);
    if (snapshot == NULL) return NULL;
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"temporary_rules\":") &&
        ch_json_append(&json, snapshot) && ch_json_append(&json, "}");
    free(snapshot);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode temporary rules payload");
    }
    return result;
}

static char *temporary_insert_json(ch_temporary_rules *rules,
                                   temporary_rule *next,
                                   long long ttl_seconds,
                                   int until_quit_pid,
                                   ch_error *error) {
    pthread_mutex_lock(&rules->mutex);
    int64_t now = temporary_now_ns();
    temporary_prune_locked(rules, now);
    uint64_t sequence = ++rules->sequence;
    char identifier[80];
    (void)snprintf(identifier, sizeof(identifier), "temporary-%" PRIu64
                   "-%" PRId64, sequence, now);
    next->id = ch_strdup(identifier);
    next->created_ns = now;
    next->expires_ns = now + ttl_seconds * INT64_C(1000000000);
    next->until_quit_pid = until_quit_pid;
    if (next->id == NULL) {
        pthread_mutex_unlock(&rules->mutex);
        temporary_rule_clear(next);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate temporary rule identifier");
        return NULL;
    }
    if (rules->count == rules->limit) {
        temporary_rule_clear(&rules->items[rules->count - 1U]);
        --rules->count;
    }
    if (rules->count > 0U) {
        memmove(rules->items + 1U, rules->items,
                rules->count * sizeof(*rules->items));
    }
    rules->items[0] = *next;
    memset(next, 0, sizeof(*next));
    ++rules->count;
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"temporary_rule\":") &&
        temporary_append_json(&json, &rules->items[0]) &&
        ch_json_append(&json, ",\"temporary_rules\":") &&
        temporary_append_snapshot_locked(&json, rules,
                                         rules->items[0].profile) &&
        ch_json_append(&json, "}");
    pthread_mutex_unlock(&rules->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode temporary rule response");
    }
    return result;
}

char *ch_temporary_rules_create_from_connection_json(
    ch_temporary_rules *rules, ch_traffic_store *traffic,
    const ch_config *config, const char *active_profile,
    const char *request_json, ch_error *error) {
    ch_error_clear(error);
    if (rules == NULL || traffic == NULL || config == NULL ||
        request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule inputs are required");
        return NULL;
    }
    char *mutation = ch_traffic_rule_request_json(
        traffic, config, active_profile, request_json, error);
    if (mutation == NULL) return NULL;
    temporary_rule next;
    long long ttl_seconds = 0;
    ch_status status = temporary_rule_from_request(
        mutation, request_json, traffic, &next, &ttl_seconds, error);
    free(mutation);
    if (status != CH_OK) return NULL;
    return temporary_insert_json(rules, &next, ttl_seconds, 0, error);
}

char *ch_temporary_rules_create_from_rule_json(
    ch_temporary_rules *rules, const char *rule_request_json,
    long long ttl_seconds, int until_quit_pid,
    const char *source_conn_id, const char *source_target,
    const char *source_target_host, ch_error *error) {
    ch_error_clear(error);
    if (rules == NULL || rule_request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule and mutation are required");
        return NULL;
    }
    if (ttl_seconds <= 0) ttl_seconds = until_quit_pid > 0 ?
        CH_TEMPORARY_RULE_MAX_TTL : CH_TEMPORARY_RULE_DEFAULT_TTL;
    if (ttl_seconds > CH_TEMPORARY_RULE_MAX_TTL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "ttl_seconds must be at most 86400");
        return NULL;
    }
    ch_json_buffer request;
    ch_json_init(&request);
    int okay = ch_json_append(&request, "{\"conn_id\":") &&
        ch_json_append_string(&request, source_conn_id == NULL ? "" :
                              source_conn_id) &&
        ch_json_append(&request, ",\"source_target\":") &&
        ch_json_append_string(&request, source_target == NULL ? "" :
                              source_target) &&
        ch_json_append(&request, ",\"source_target_host\":") &&
        ch_json_append_string(&request, source_target_host == NULL ? "" :
                              source_target_host) &&
        ch_json_append_format(&request, ",\"ttl_seconds\":%lld}",
                              ttl_seconds);
    char *request_json = okay ? ch_json_take(&request) : NULL;
    ch_json_dispose(&request);
    if (request_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode temporary rule source");
        return NULL;
    }
    temporary_rule next;
    long long parsed_ttl = 0;
    ch_status status = temporary_rule_from_request(
        rule_request_json, request_json, NULL, &next, &parsed_ttl, error);
    free(request_json);
    if (status != CH_OK) return NULL;
    return temporary_insert_json(rules, &next, parsed_ttl,
                                 until_quit_pid, error);
}

char *ch_temporary_rules_remove_json(ch_temporary_rules *rules,
                                     const char *request_json,
                                     ch_error *error) {
    ch_error_clear(error);
    if (rules == NULL || request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule manager and request are required");
        return NULL;
    }
    ch_json_value *request = ch_json_parse(
        request_json, strlen(request_json), error);
    if (request == NULL) return NULL;
    char *id = temporary_request_string(request, "id");
    ch_json_value_destroy(request);
    if (id == NULL || id[0] == '\0') {
        free(id);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule identifier is required");
        return NULL;
    }
    pthread_mutex_lock(&rules->mutex);
    temporary_prune_locked(rules, temporary_now_ns());
    size_t index = 0U;
    while (index < rules->count &&
           strcmp(rules->items[index].id, id) != 0) ++index;
    free(id);
    if (index == rules->count) {
        pthread_mutex_unlock(&rules->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "temporary rule not found");
        return NULL;
    }
    char *profile = ch_strdup(rules->items[index].profile);
    if (profile == NULL) {
        pthread_mutex_unlock(&rules->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy temporary rule profile");
        return NULL;
    }
    temporary_rule_clear(&rules->items[index]);
    if (index + 1U < rules->count) {
        memmove(&rules->items[index], &rules->items[index + 1U],
                (rules->count - index - 1U) * sizeof(*rules->items));
    }
    --rules->count;
    memset(&rules->items[rules->count], 0, sizeof(*rules->items));
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"temporary_rules\":") &&
        temporary_append_snapshot_locked(&json, rules, profile) &&
        ch_json_append(&json, "}");
    pthread_mutex_unlock(&rules->mutex);
    free(profile);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode temporary rule removal");
    }
    return result;
}

static void temporary_split_target(const char *target, char **host,
                                   char **port) {
    *host = NULL;
    *port = NULL;
    if (target == NULL) return;
    const char *host_start = target;
    const char *host_end = NULL;
    const char *port_start = NULL;
    if (target[0] == '[') {
        host_start = target + 1U;
        host_end = strchr(host_start, ']');
        if (host_end != NULL && host_end[1] == ':') port_start = host_end + 2U;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator != NULL && strchr(target, ':') == separator) {
            host_end = separator;
            port_start = separator + 1U;
        }
    }
    if (host_end == NULL) host_end = target + strlen(target);
    size_t host_length = (size_t)(host_end - host_start);
    *host = malloc(host_length + 1U);
    if (*host != NULL) {
        memcpy(*host, host_start, host_length);
        (*host)[host_length] = '\0';
        for (size_t index = 0U; index < host_length; ++index) {
            (*host)[index] = (char)tolower((unsigned char)(*host)[index]);
        }
    }
    *port = ch_strdup(port_start == NULL ? "" : port_start);
}

static int temporary_cidr_matches(const char *cidr, const char *host) {
    char *copy = ch_strdup(cidr);
    if (copy == NULL) return 0;
    char *slash = strrchr(copy, '/');
    if (slash == NULL) {
        free(copy);
        return 0;
    }
    *slash++ = '\0';
    char *end = NULL;
    long bits = strtol(slash, &end, 10);
    uint8_t network[16], address[16];
    int family = strchr(copy, ':') == NULL ? AF_INET : AF_INET6;
    size_t length = family == AF_INET ? 4U : 16U;
    long maximum = family == AF_INET ? 32L : 128L;
    int matches = end != slash && *end == '\0' && bits >= 0 &&
        bits <= maximum && inet_pton(family, copy, network) == 1 &&
        inet_pton(family, host, address) == 1;
    if (matches) {
        size_t whole = (size_t)bits / 8U;
        unsigned int remainder = (unsigned int)bits % 8U;
        if (whole > 0U && memcmp(network, address, whole) != 0) matches = 0;
        if (matches && remainder > 0U && whole < length) {
            uint8_t mask = (uint8_t)(0xffU << (8U - remainder));
            matches = (network[whole] & mask) == (address[whole] & mask);
        }
    }
    free(copy);
    return matches;
}

static int temporary_matches(const temporary_rule *rule, const char *host) {
    if (rule->match_kind == TEMPORARY_MATCH_NONE) return 1;
    if (rule->match_kind == TEMPORARY_MATCH_DOMAIN) {
        return strcasecmp(rule->match_value, host) == 0;
    }
    if (rule->match_kind == TEMPORARY_MATCH_SUFFIX) {
        size_t host_length = strlen(host);
        size_t suffix_length = strlen(rule->match_value);
        return host_length == suffix_length ?
            strcasecmp(host, rule->match_value) == 0 :
            host_length > suffix_length &&
            host[host_length - suffix_length - 1U] == '.' &&
            strcasecmp(host + host_length - suffix_length,
                       rule->match_value) == 0;
    }
    return temporary_cidr_matches(rule->match_value, host);
}

bool ch_temporary_rules_needs_process(ch_temporary_rules *rules) {
    if (rules == NULL) return false;
    pthread_mutex_lock(&rules->mutex);
    temporary_prune_locked(rules, temporary_now_ns());
    bool needed = false;
    for (size_t index = 0U; index < rules->count; ++index) {
        if (rules->items[index].process[0] != '\0') {
            needed = true;
            break;
        }
    }
    pthread_mutex_unlock(&rules->mutex);
    return needed;
}

static int temporary_decision_copy(ch_rule_decision *decision,
                                   const temporary_rule *rule,
                                   const ch_rule_match_context *context,
                                   const char *host, const char *port,
                                   uint64_t elapsed) {
    const char *action = rule->action;
    const char *chain = "";
    const char *group = "";
    char action_name[16];
    if (strncasecmp(action, "chain:", 6U) == 0) {
        (void)snprintf(action_name, sizeof(action_name), "chain");
        chain = action + 6U;
    } else if (strncasecmp(action, "group:", 6U) == 0) {
        (void)snprintf(action_name, sizeof(action_name), "group");
        group = action + 6U;
    } else {
        (void)snprintf(action_name, sizeof(action_name), "%s", action);
    }
    const char *kind = rule->match_kind == TEMPORARY_MATCH_DOMAIN ?
        "domain" : rule->match_kind == TEMPORARY_MATCH_SUFFIX ?
        "domain_suffix" : rule->match_kind == TEMPORARY_MATCH_CIDR ?
        "cidr" : "process";
    const char *matched_value = rule->match_kind == TEMPORARY_MATCH_NONE ?
        rule->process : rule->match_value;
    char summary[512];
    (void)snprintf(summary, sizeof(summary),
                   "Temporary rule \"%s\" matched %s \"%s\".",
                   rule->name, kind, matched_value);
    memset(decision, 0, sizeof(*decision));
    int copied = temporary_copy(&decision->rule_name, rule->name) &&
        temporary_copy(&decision->action, action_name) &&
        temporary_copy(&decision->chain_name, chain) &&
        temporary_copy(&decision->group_name, group) &&
        temporary_copy(&decision->target, context->target) &&
        temporary_copy(&decision->host, host) &&
        temporary_copy(&decision->port, port) &&
        temporary_copy(&decision->network, context->network) &&
        temporary_copy(&decision->source, context->source) &&
        temporary_copy(&decision->matcher_kind, kind) &&
        temporary_copy(&decision->matcher_value, matched_value) &&
        temporary_copy(&decision->summary, summary);
    if (!copied) {
        ch_rule_decision_clear(decision);
        return 0;
    }
    decision->rule_number = 1U;
    decision->elapsed_ns = elapsed > (uint64_t)INT64_MAX ? INT64_MAX :
                                                             (long long)elapsed;
    return 1;
}

ch_status ch_temporary_rules_decide(
    ch_temporary_rules *rules, const char *profile,
    const ch_rule_match_context *context, ch_rule_decision *decision,
    bool *matched, ch_error *error) {
    ch_error_clear(error);
    if (rules == NULL || profile == NULL || context == NULL ||
        context->target == NULL || decision == NULL || matched == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "temporary rule decision inputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *matched = false;
    memset(decision, 0, sizeof(*decision));
    char *host = NULL, *port = NULL;
    temporary_split_target(context->target, &host, &port);
    if (host == NULL || port == NULL) {
        free(host);
        free(port);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "parse temporary rule target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    uint64_t started = temporary_monotonic_ns();
    pthread_mutex_lock(&rules->mutex);
    temporary_prune_locked(rules, temporary_now_ns());
    for (size_t index = 0U; index < rules->count; ++index) {
        temporary_rule *rule = &rules->items[index];
        int process_matches = rule->process[0] == '\0' ||
            (context->process_path != NULL &&
             strcmp(rule->process, context->process_path) == 0) ||
            (context->process_name != NULL &&
             strcmp(rule->process, context->process_name) == 0);
        int network_matches = rule->network[0] == '\0' ||
            (context->network != NULL &&
             strcasecmp(rule->network, context->network) == 0);
        int port_matches = rule->port[0] == '\0' ||
            strcmp(rule->port, port) == 0;
        if (strcmp(rule->profile, profile) != 0 || !process_matches ||
            !network_matches || !port_matches ||
            !temporary_matches(rule, host)) continue;
        uint64_t finished = temporary_monotonic_ns();
        int copied = temporary_decision_copy(
            decision, rule, context, host, port,
            finished >= started ? finished - started : 0U);
        pthread_mutex_unlock(&rules->mutex);
        free(host);
        free(port);
        if (!copied) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy temporary rule decision");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        *matched = true;
        return CH_OK;
    }
    pthread_mutex_unlock(&rules->mutex);
    free(host);
    free(port);
    return CH_OK;
}
