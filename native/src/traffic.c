#include "clambhook/traffic.h"

#include <ctype.h>
#include <inttypes.h>
#include <arpa/inet.h>
#include <limits.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "internal.h"

#define CH_TRAFFIC_DEFAULT_LIMIT 512U
#define CH_TRAFFIC_MAX_LIMIT 4096U
#define CH_TRAFFIC_QUERY_LIMIT 1000U

typedef struct ch_traffic_entry {
    uint64_t flow_id;
    char *conn_id;
    char *profile;
    char *listener_protocol;
    char *listener_address;
    char *client_address;
    char *chain_name;
    char *group_name;
    char *rule_name;
    char *rule_action;
    char *target;
    char *target_host;
    char *target_port;
    char *network;
    char *source;
    char *close_reason;
    bool is_default;
    bool active;
    long long decision_ns;
    int64_t start_ns;
    int64_t updated_ns;
    int64_t end_ns;
    int64_t rate_sample_ns;
    uint64_t rx_total;
    uint64_t tx_total;
    double rx_bps;
    double tx_bps;
} ch_traffic_entry;

struct ch_traffic_store {
    pthread_mutex_t mutex;
    ch_traffic_entry *entries;
    size_t count;
    size_t limit;
    uint64_t sequence;
};

typedef struct ch_traffic_filter {
    char *state;
    char *action;
    char *profile;
    char *rule;
    char *port;
    char *network;
    char *domain;
    char *query;
    size_t limit;
    size_t offset;
} ch_traffic_filter;

static int64_t traffic_now_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (int64_t)now.tv_sec * INT64_C(1000000000) +
        (int64_t)now.tv_nsec;
}

static void traffic_entry_clear(ch_traffic_entry *entry) {
    if (entry == NULL) return;
    free(entry->conn_id);
    free(entry->profile);
    free(entry->listener_protocol);
    free(entry->listener_address);
    free(entry->client_address);
    free(entry->chain_name);
    free(entry->group_name);
    free(entry->rule_name);
    free(entry->rule_action);
    free(entry->target);
    free(entry->target_host);
    free(entry->target_port);
    free(entry->network);
    free(entry->source);
    free(entry->close_reason);
    memset(entry, 0, sizeof(*entry));
}

static int traffic_copy(char **out, const char *value) {
    *out = ch_strdup(value == NULL ? "" : value);
    return *out != NULL;
}

static int traffic_entry_initialize(ch_traffic_entry *entry,
                                    uint64_t flow_id,
                                    const ch_traffic_open_info *info) {
    memset(entry, 0, sizeof(*entry));
    char identifier[40];
    (void)snprintf(identifier, sizeof(identifier), "native-%" PRIu64,
                   flow_id);
    if (!traffic_copy(&entry->conn_id, identifier) ||
        !traffic_copy(&entry->profile, info->profile) ||
        !traffic_copy(&entry->listener_protocol,
                      info->listener_protocol) ||
        !traffic_copy(&entry->listener_address, info->listener_address) ||
        !traffic_copy(&entry->client_address, info->client_address) ||
        !traffic_copy(&entry->chain_name, info->chain_name) ||
        !traffic_copy(&entry->group_name, info->group_name) ||
        !traffic_copy(&entry->rule_name, info->rule_name) ||
        !traffic_copy(&entry->rule_action, info->rule_action) ||
        !traffic_copy(&entry->target, info->target) ||
        !traffic_copy(&entry->target_host, info->target_host) ||
        !traffic_copy(&entry->target_port, info->target_port) ||
        !traffic_copy(&entry->network, info->network) ||
        !traffic_copy(&entry->source, info->source) ||
        !traffic_copy(&entry->close_reason, "")) {
        traffic_entry_clear(entry);
        return 0;
    }
    int64_t now = traffic_now_ns();
    entry->flow_id = flow_id;
    entry->is_default = info->is_default;
    entry->decision_ns = info->decision_ns;
    entry->active = true;
    entry->start_ns = now;
    entry->updated_ns = now;
    entry->rate_sample_ns = now;
    return 1;
}

ch_traffic_store *ch_traffic_store_create(size_t history_limit,
                                          ch_error *error) {
    ch_error_clear(error);
    size_t limit = history_limit == 0U ? CH_TRAFFIC_DEFAULT_LIMIT :
                                        history_limit;
    if (limit > CH_TRAFFIC_MAX_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic history limit exceeds %u",
                     (unsigned int)CH_TRAFFIC_MAX_LIMIT);
        return NULL;
    }
    ch_traffic_store *store = calloc(1U, sizeof(*store));
    if (store == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate traffic store");
        return NULL;
    }
    store->entries = calloc(limit, sizeof(*store->entries));
    if (store->entries == NULL ||
        pthread_mutex_init(&store->mutex, NULL) != 0) {
        free(store->entries);
        free(store);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize traffic store");
        return NULL;
    }
    store->limit = limit;
    return store;
}

void ch_traffic_store_destroy(ch_traffic_store *store) {
    if (store == NULL) return;
    pthread_mutex_lock(&store->mutex);
    for (size_t index = 0U; index < store->count; ++index) {
        traffic_entry_clear(&store->entries[index]);
    }
    free(store->entries);
    store->entries = NULL;
    store->count = 0U;
    pthread_mutex_unlock(&store->mutex);
    pthread_mutex_destroy(&store->mutex);
    free(store);
}

uint64_t ch_traffic_open(ch_traffic_store *store,
                         const ch_traffic_open_info *info,
                         ch_error *error) {
    ch_error_clear(error);
    if (store == NULL || info == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic store and open information are required");
        return 0U;
    }
    pthread_mutex_lock(&store->mutex);
    uint64_t flow_id = ++store->sequence;
    if (flow_id == 0U) flow_id = ++store->sequence;
    ch_traffic_entry next;
    if (!traffic_entry_initialize(&next, flow_id, info)) {
        pthread_mutex_unlock(&store->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy traffic connection");
        return 0U;
    }
    if (store->count == store->limit) {
        traffic_entry_clear(&store->entries[store->count - 1U]);
        --store->count;
    }
    if (store->count > 0U) {
        memmove(store->entries + 1U, store->entries,
                store->count * sizeof(*store->entries));
    }
    store->entries[0] = next;
    ++store->count;
    pthread_mutex_unlock(&store->mutex);
    return flow_id;
}

static ch_traffic_entry *traffic_find_flow(ch_traffic_store *store,
                                           uint64_t flow_id) {
    for (size_t index = 0U; index < store->count; ++index) {
        if (store->entries[index].flow_id == flow_id) {
            return &store->entries[index];
        }
    }
    return NULL;
}

void ch_traffic_bytes(ch_traffic_store *store, uint64_t flow_id,
                      uint64_t rx_delta, uint64_t tx_delta) {
    if (store == NULL || flow_id == 0U ||
        (rx_delta == 0U && tx_delta == 0U)) return;
    pthread_mutex_lock(&store->mutex);
    ch_traffic_entry *entry = traffic_find_flow(store, flow_id);
    if (entry != NULL) {
        int64_t now = traffic_now_ns();
        int64_t interval = now - entry->rate_sample_ns;
        entry->rx_total += rx_delta;
        entry->tx_total += tx_delta;
        if (interval > 0) {
            double seconds = (double)interval / 1000000000.0;
            entry->rx_bps = (double)rx_delta / seconds;
            entry->tx_bps = (double)tx_delta / seconds;
        }
        entry->updated_ns = now;
        entry->rate_sample_ns = now;
    }
    pthread_mutex_unlock(&store->mutex);
}

static void traffic_close_entry(ch_traffic_entry *entry, const char *reason,
                                int64_t now) {
    if (entry == NULL || !entry->active) return;
    char *copy = ch_strdup(reason == NULL ? "closed" : reason);
    if (copy != NULL) {
        free(entry->close_reason);
        entry->close_reason = copy;
    }
    entry->active = false;
    entry->end_ns = now;
    entry->updated_ns = now;
    entry->rx_bps = 0.0;
    entry->tx_bps = 0.0;
}

void ch_traffic_close(ch_traffic_store *store, uint64_t flow_id,
                      const char *reason) {
    if (store == NULL || flow_id == 0U) return;
    pthread_mutex_lock(&store->mutex);
    traffic_close_entry(traffic_find_flow(store, flow_id), reason,
                        traffic_now_ns());
    pthread_mutex_unlock(&store->mutex);
}

void ch_traffic_close_all(ch_traffic_store *store, const char *reason) {
    if (store == NULL) return;
    pthread_mutex_lock(&store->mutex);
    int64_t now = traffic_now_ns();
    for (size_t index = 0U; index < store->count; ++index) {
        traffic_close_entry(&store->entries[index], reason, now);
    }
    pthread_mutex_unlock(&store->mutex);
}

void ch_traffic_connection_clear(ch_traffic_connection *connection) {
    if (connection == NULL) return;
    free(connection->conn_id);
    free(connection->profile);
    free(connection->chain_name);
    free(connection->group_name);
    free(connection->rule_name);
    free(connection->rule_action);
    free(connection->target);
    free(connection->target_host);
    free(connection->target_port);
    free(connection->network);
    free(connection->source);
    memset(connection, 0, sizeof(*connection));
}

static int traffic_connection_copy_entry(const ch_traffic_entry *entry,
                                         ch_traffic_connection *out) {
    memset(out, 0, sizeof(*out));
    if (!traffic_copy(&out->conn_id, entry->conn_id) ||
        !traffic_copy(&out->profile, entry->profile) ||
        !traffic_copy(&out->chain_name, entry->chain_name) ||
        !traffic_copy(&out->group_name, entry->group_name) ||
        !traffic_copy(&out->rule_name, entry->rule_name) ||
        !traffic_copy(&out->rule_action, entry->rule_action) ||
        !traffic_copy(&out->target, entry->target) ||
        !traffic_copy(&out->target_host, entry->target_host) ||
        !traffic_copy(&out->target_port, entry->target_port) ||
        !traffic_copy(&out->network, entry->network) ||
        !traffic_copy(&out->source, entry->source)) {
        ch_traffic_connection_clear(out);
        return 0;
    }
    out->is_default = entry->is_default;
    return 1;
}

ch_status ch_traffic_connection_copy(ch_traffic_store *store,
                                     const char *conn_id,
                                     ch_traffic_connection *out_connection,
                                     ch_error *error) {
    ch_error_clear(error);
    if (store == NULL || conn_id == NULL || conn_id[0] == '\0' ||
        out_connection == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic connection identifier is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_connection, 0, sizeof(*out_connection));
    pthread_mutex_lock(&store->mutex);
    for (size_t index = 0U; index < store->count; ++index) {
        if (strcmp(store->entries[index].conn_id, conn_id) != 0) continue;
        int copied = traffic_connection_copy_entry(&store->entries[index],
                                                   out_connection);
        pthread_mutex_unlock(&store->mutex);
        if (!copied) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy traffic connection");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        return CH_OK;
    }
    pthread_mutex_unlock(&store->mutex);
    ch_error_set(error, CH_ERROR_NOT_FOUND, "connection not found");
    return CH_ERROR_NOT_FOUND;
}

static void traffic_filter_clear(ch_traffic_filter *filter) {
    free(filter->state);
    free(filter->action);
    free(filter->profile);
    free(filter->rule);
    free(filter->port);
    free(filter->network);
    free(filter->domain);
    free(filter->query);
    memset(filter, 0, sizeof(*filter));
}

static int traffic_filter_string(const ch_json_value *root, const char *key,
                                 char **out) {
    const ch_json_value *value = ch_json_object_get(root, key);
    const char *text = ch_json_string_value(value);
    *out = ch_strdup(text == NULL ? "" : text);
    return *out != NULL;
}

static ch_status traffic_filter_parse(const char *json,
                                      ch_traffic_filter *filter,
                                      ch_error *error) {
    memset(filter, 0, sizeof(*filter));
    filter->limit = 200U;
    if (json == NULL || json[0] == '\0') json = "{}";
    ch_json_value *root = ch_json_parse(json, strlen(json), error);
    if (root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(root) != CH_JSON_OBJECT ||
        !traffic_filter_string(root, "state", &filter->state) ||
        !traffic_filter_string(root, "action", &filter->action) ||
        !traffic_filter_string(root, "profile", &filter->profile) ||
        !traffic_filter_string(root, "rule", &filter->rule) ||
        !traffic_filter_string(root, "port", &filter->port) ||
        !traffic_filter_string(root, "network", &filter->network) ||
        !traffic_filter_string(root, "domain", &filter->domain) ||
        !traffic_filter_string(root, "query", &filter->query)) {
        ch_json_value_destroy(root);
        traffic_filter_clear(filter);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic filter must be a JSON object");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    double limit = ch_json_number_value(ch_json_object_get(root, "limit"),
                                        200.0);
    double offset = ch_json_number_value(ch_json_object_get(root, "offset"),
                                         0.0);
    if (limit > 0.0) {
        filter->limit = limit > (double)CH_TRAFFIC_QUERY_LIMIT ?
            CH_TRAFFIC_QUERY_LIMIT : (size_t)limit;
    }
    if (offset > 0.0) filter->offset = (size_t)offset;
    ch_json_value_destroy(root);
    return CH_OK;
}

static int traffic_contains_fold(const char *text, const char *needle) {
    if (needle == NULL || needle[0] == '\0') return 1;
    if (text == NULL) return 0;
    size_t needle_length = strlen(needle);
    for (const char *cursor = text; *cursor != '\0'; ++cursor) {
        size_t index = 0U;
        while (index < needle_length && cursor[index] != '\0' &&
               tolower((unsigned char)cursor[index]) ==
                   tolower((unsigned char)needle[index])) {
            ++index;
        }
        if (index == needle_length) return 1;
    }
    return 0;
}

static const char *traffic_action_family(const char *action) {
    if (action != NULL &&
        (strcasecmp(action, "block") == 0 ||
         strcasecmp(action, "reject") == 0)) return "block";
    if (action != NULL && strcasecmp(action, "direct") == 0) return "direct";
    return "proxy";
}

static int traffic_entry_matches(const ch_traffic_entry *entry,
                                 const ch_traffic_filter *filter) {
    if (filter->state[0] != '\0' && strcasecmp(filter->state, "all") != 0) {
        const char *state = entry->active ? "active" : "closed";
        if (strcasecmp(filter->state, state) != 0) return 0;
    }
    if (filter->action[0] != '\0' &&
        strcasecmp(filter->action,
                   traffic_action_family(entry->rule_action)) != 0 &&
        strcasecmp(filter->action, entry->rule_action) != 0) return 0;
    if (filter->profile[0] != '\0' &&
        strcmp(filter->profile, entry->profile) != 0) return 0;
    if (filter->rule[0] != '\0' &&
        strcmp(filter->rule, entry->rule_name) != 0) return 0;
    if (filter->port[0] != '\0' &&
        strcmp(filter->port, entry->target_port) != 0) return 0;
    if (filter->network[0] != '\0' &&
        strcasecmp(filter->network, entry->network) != 0) return 0;
    if (filter->domain[0] != '\0' &&
        !traffic_contains_fold(entry->target_host, filter->domain)) return 0;
    if (filter->query[0] != '\0' &&
        !traffic_contains_fold(entry->target, filter->query) &&
        !traffic_contains_fold(entry->target_host, filter->query) &&
        !traffic_contains_fold(entry->rule_name, filter->query) &&
        !traffic_contains_fold(entry->chain_name, filter->query)) return 0;
    return 1;
}

static int traffic_append_connection(ch_json_buffer *json,
                                     const ch_traffic_entry *entry,
                                     int64_t now) {
    int64_t duration = (entry->active ? now : entry->end_ns) -
        entry->start_ns;
    if (duration < 0) duration = 0;
    if (!ch_json_append(json, "{\"conn_id\":" ) ||
        !ch_json_append_string(json, entry->conn_id) ||
        !ch_json_append(json, ",\"profile\":") ||
        !ch_json_append_string(json, entry->profile) ||
        !ch_json_append(json, ",\"state\":") ||
        !ch_json_append_string(json, entry->active ? "active" : "closed") ||
        !ch_json_append_format(json,
            ",\"start_ts_ns\":%" PRId64
            ",\"updated_ts_ns\":%" PRId64
            ",\"end_ts_ns\":%" PRId64,
            entry->start_ns, entry->updated_ns, entry->end_ns) ||
        !ch_json_append(json, ",\"listener\":{\"protocol\":") ||
        !ch_json_append_string(json, entry->listener_protocol) ||
        !ch_json_append(json, ",\"addr\":") ||
        !ch_json_append_string(json, entry->listener_address) ||
        !ch_json_append(json, "},\"client_addr\":") ||
        !ch_json_append_string(json, entry->client_address) ||
        !ch_json_append(json, ",\"chain_name\":") ||
        !ch_json_append_string(json, entry->chain_name) ||
        !ch_json_append(json, ",\"group_name\":") ||
        !ch_json_append_string(json, entry->group_name) ||
        !ch_json_append(json, ",\"rule_name\":") ||
        !ch_json_append_string(json, entry->rule_name) ||
        !ch_json_append(json, ",\"rule_action\":") ||
        !ch_json_append_string(json, entry->rule_action) ||
        !ch_json_append_format(json, ",\"default\":%s,\"decision_ns\":%lld",
                               entry->is_default ? "true" : "false",
                               entry->decision_ns) ||
        !ch_json_append(json, ",\"target\":") ||
        !ch_json_append_string(json, entry->target) ||
        !ch_json_append(json, ",\"target_host\":") ||
        !ch_json_append_string(json, entry->target_host) ||
        !ch_json_append(json, ",\"target_port\":") ||
        !ch_json_append_string(json, entry->target_port) ||
        !ch_json_append(json, ",\"network\":") ||
        !ch_json_append_string(json, entry->network) ||
        !ch_json_append(json, ",\"source\":") ||
        !ch_json_append_string(json, entry->source) ||
        !ch_json_append(json, ",\"application\":\"\",\"hops\":[],") ||
        !ch_json_append(json, "\"timeline\":[{\"ts_ns\":") ||
        !ch_json_append_format(json, "%" PRId64, entry->start_ns) ||
        !ch_json_append(json,
            ",\"type\":\"connection_opened\",\"title\":\"Opened\","
            "\"detail\":\"\"}],\"geo\":{},\"geo_error\":\"\",") ||
        !ch_json_append_format(json,
            "\"total_dial_ns\":0,\"rx_bps\":%.3f,\"tx_bps\":%.3f,"
            "\"rx_total\":%" PRIu64 ",\"tx_total\":%" PRIu64
            ",\"duration_ns\":%" PRId64 ",\"close_reason\":",
            entry->rx_bps, entry->tx_bps, entry->rx_total, entry->tx_total,
            duration) ||
        !ch_json_append_string(json, entry->close_reason) ||
        !ch_json_append(json, "}")) return 0;
    return 1;
}

static int traffic_append_profiles(ch_json_buffer *json,
                                   const ch_config *config,
                                   const char *active_profile) {
    if (!ch_json_append(json, "\"profile_context\":{\"active\":" ) ||
        !ch_json_append_string(json, active_profile) ||
        !ch_json_append(json, ",\"profiles\":[")) return 0;
    if (config == NULL) {
        if (!ch_json_append_string(json, active_profile)) return 0;
    } else {
        size_t count = ch_config_profile_count(config);
        for (size_t index = 0U; index < count; ++index) {
            char *name = NULL;
            ch_error ignored;
            if (ch_config_table_get_string(ch_config_profile_at(config, index),
                                           "name", &name, &ignored) != CH_OK ||
                (index > 0U && !ch_json_append(json, ",")) ||
                !ch_json_append_string(json, name)) {
                free(name);
                return 0;
            }
            free(name);
        }
    }
    return ch_json_append(json, "]}");
}

static int traffic_append_quick_filters(ch_json_buffer *json,
                                        const ch_traffic_store *store) {
    size_t active = 0U, proxy = 0U, direct = 0U, block = 0U;
    for (size_t index = 0U; index < store->count; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        if (entry->active) ++active;
        const char *family = traffic_action_family(entry->rule_action);
        if (strcmp(family, "block") == 0) ++block;
        else if (strcmp(family, "direct") == 0) ++direct;
        else ++proxy;
    }
    return ch_json_append_format(json,
        "\"quick_filters\":["
        "{\"key\":\"all\",\"label\":\"All\",\"count\":%zu},"
        "{\"key\":\"active\",\"label\":\"Active\",\"count\":%zu},"
        "{\"key\":\"proxy\",\"label\":\"Proxy\",\"count\":%zu},"
        "{\"key\":\"direct\",\"label\":\"Direct\",\"count\":%zu},"
        "{\"key\":\"block\",\"label\":\"Block\",\"count\":%zu}]",
        store->count, active, proxy, direct, block);
}

static int traffic_append_rule_hits(ch_json_buffer *json,
                                    const ch_traffic_store *store) {
    if (!ch_json_append(json, "\"rule_hits\":[")) return 0;
    int first = 1;
    for (size_t index = 0U; index < store->count; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        int seen = 0;
        for (size_t prior = 0U; prior < index; ++prior) {
            const ch_traffic_entry *candidate = &store->entries[prior];
            if (strcmp(candidate->profile, entry->profile) == 0 &&
                strcmp(candidate->rule_name, entry->rule_name) == 0 &&
                strcmp(traffic_action_family(candidate->rule_action),
                       traffic_action_family(entry->rule_action)) == 0) {
                seen = 1;
                break;
            }
        }
        if (seen) continue;
        size_t count = 0U;
        uint64_t rx = 0U, tx = 0U;
        int64_t last = 0;
        const char *last_target = "";
        for (size_t nested = index; nested < store->count; ++nested) {
            const ch_traffic_entry *candidate = &store->entries[nested];
            if (strcmp(candidate->profile, entry->profile) != 0 ||
                strcmp(candidate->rule_name, entry->rule_name) != 0 ||
                strcmp(traffic_action_family(candidate->rule_action),
                       traffic_action_family(entry->rule_action)) != 0) continue;
            ++count;
            rx += candidate->rx_total;
            tx += candidate->tx_total;
            if (candidate->updated_ns >= last) {
                last = candidate->updated_ns;
                last_target = candidate->target;
            }
        }
        if (!first && !ch_json_append(json, ",")) return 0;
        first = 0;
        if (!ch_json_append(json, "{\"profile\":") ||
            !ch_json_append_string(json, entry->profile) ||
            !ch_json_append(json, ",\"rule_name\":") ||
            !ch_json_append_string(json, entry->rule_name[0] == '\0' &&
                entry->is_default ? "default" : entry->rule_name) ||
            !ch_json_append(json, ",\"action\":") ||
            !ch_json_append_string(json,
                                   traffic_action_family(entry->rule_action)) ||
            !ch_json_append_format(json,
                ",\"count\":%zu,\"last_hit_ts_ns\":%" PRId64
                ",\"rx_total\":%" PRIu64 ",\"tx_total\":%" PRIu64
                ",\"last_target\":", count, last, rx, tx) ||
            !ch_json_append_string(json, last_target) ||
            !ch_json_append_format(json, ",\"default\":%s}",
                                   entry->is_default ? "true" : "false")) {
            return 0;
        }
    }
    return ch_json_append(json, "]");
}

static int traffic_append_blocks(ch_json_buffer *json,
                                 const ch_traffic_store *store) {
    if (!ch_json_append(json, "\"block_decisions\":[")) return 0;
    size_t written = 0U;
    for (size_t index = 0U; index < store->count && written < 12U; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        if (strcmp(traffic_action_family(entry->rule_action), "block") != 0)
            continue;
        if (written > 0U && !ch_json_append(json, ",")) return 0;
        if (!ch_json_append(json, "{\"conn_id\":") ||
            !ch_json_append_string(json, entry->conn_id) ||
            !ch_json_append(json, ",\"profile\":") ||
            !ch_json_append_string(json, entry->profile) ||
            !ch_json_append(json, ",\"rule_name\":") ||
            !ch_json_append_string(json, entry->rule_name) ||
            !ch_json_append(json, ",\"action\":") ||
            !ch_json_append_string(json, entry->rule_action) ||
            !ch_json_append(json, ",\"target\":") ||
            !ch_json_append_string(json, entry->target) ||
            !ch_json_append(json, ",\"target_host\":") ||
            !ch_json_append_string(json, entry->target_host) ||
            !ch_json_append(json, ",\"target_port\":") ||
            !ch_json_append_string(json, entry->target_port) ||
            !ch_json_append(json, ",\"network\":") ||
            !ch_json_append_string(json, entry->network) ||
            !ch_json_append_format(json, ",\"ts_ns\":%" PRId64,
                                   entry->updated_ns) ||
            !ch_json_append(json, ",\"close_reason\":") ||
            !ch_json_append_string(json, entry->close_reason) ||
            !ch_json_append(json, "}")) return 0;
        ++written;
    }
    return ch_json_append(json, "]");
}

static char *traffic_config_string(const ch_config_table *table,
                                   const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || ch_config_table_get_string(
            table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static int traffic_rule_has_matchers(const ch_config_table *rule) {
    static const char *keys[] = {
        "rule_sets", "domains", "domain_suffixes", "domain_keywords",
        "cidrs", "source_cidrs", "ports", "networks", "processes"
    };
    for (size_t index = 0U; index < sizeof(keys) / sizeof(keys[0]); ++index) {
        if (ch_config_array_count(ch_config_table_get_array(rule,
                                                            keys[index])) > 0U)
            return 1;
    }
    return 0;
}

static int traffic_rule_was_hit(const ch_traffic_store *store,
                                const char *profile, const char *name) {
    for (size_t index = 0U; index < store->count; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        if ((profile[0] == '\0' || entry->profile[0] == '\0' ||
             strcmp(entry->profile, profile) == 0) &&
            strcmp(entry->rule_name, name) == 0) return 1;
    }
    return 0;
}

static int traffic_append_cleanup_suggestions(
    ch_json_buffer *json, const ch_traffic_store *store,
    const ch_config *config, const char *profile_name) {
    if (!ch_json_append(json, "\"cleanup_suggestions\":[")) return 0;
    const ch_config_table *profile = config == NULL ? NULL :
        ch_config_profile_named(config, profile_name);
    const ch_config_array *rules = ch_config_table_get_array(profile, "rule");
    size_t count = ch_config_array_count(rules);
    size_t written = 0U;
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *rule = ch_config_array_get_table(rules, index);
        char *name = traffic_config_string(rule, "name");
        char *action = traffic_config_string(rule, "action");
        if (name == NULL || action == NULL) {
            free(name);
            free(action);
            return 0;
        }
        int broad_first = index == 0U && !traffic_rule_has_matchers(rule);
        const char *kind = NULL;
        const char *operation = NULL;
        const char *message = NULL;
        if (broad_first) {
            kind = "broad_match";
            operation = "move_rule_to_end";
            message = "First rule has no matchers and may shadow every later rule.";
        } else if (!traffic_rule_was_hit(store, profile_name, name)) {
            kind = "unused_in_history";
            operation = "delete_rule";
            message = "No recent traffic-history entries matched this rule.";
        }
        if (kind != NULL) {
            if (written > 0U && !ch_json_append(json, ",")) {
                free(name);
                free(action);
                return 0;
            }
            if (!ch_json_append(json, "{\"kind\":") ||
                !ch_json_append_string(json, kind) ||
                !ch_json_append(json, ",\"profile\":") ||
                !ch_json_append_string(json, profile_name) ||
                !ch_json_append(json, ",\"rule_name\":") ||
                !ch_json_append_string(json, name) ||
                !ch_json_append(json, ",\"target_rule_name\":") ||
                !ch_json_append_string(json, name) ||
                !ch_json_append(json, ",\"operation\":") ||
                !ch_json_append_string(json, operation) ||
                !ch_json_append(json, ",\"action\":") ||
                !ch_json_append_string(json, action) ||
                !ch_json_append(json, ",\"message\":") ||
                !ch_json_append_string(json, message) ||
                !ch_json_append(json,
                    ",\"count\":0,\"last_hit_ts_ns\":0}")) {
                free(name);
                free(action);
                return 0;
            }
            ++written;
        }
        free(name);
        free(action);
    }
    return ch_json_append(json, "]");
}

char *ch_traffic_snapshot_json(ch_traffic_store *store,
                               const ch_config *config,
                               const char *active_profile,
                               const char *filter_json,
                               const char *temporary_rules_json,
                               ch_error *error) {
    ch_error_clear(error);
    if (store == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic store is required");
        return NULL;
    }
    ch_traffic_filter filter;
    if (traffic_filter_parse(filter_json, &filter, error) != CH_OK) return NULL;
    if (active_profile == NULL) active_profile = "";
    if (temporary_rules_json == NULL || temporary_rules_json[0] == '\0') {
        temporary_rules_json = "[]";
    }
    ch_json_value *temporary_rules = ch_json_parse(
        temporary_rules_json, strlen(temporary_rules_json), error);
    if (temporary_rules == NULL ||
        ch_json_value_type(temporary_rules) != CH_JSON_ARRAY) {
        ch_json_value_destroy(temporary_rules);
        traffic_filter_clear(&filter);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "temporary rules must be a JSON array");
        }
        return NULL;
    }
    /* Preserve exact int64 nanosecond timestamps from the validated payload. */
    ch_json_value_destroy(temporary_rules);
    temporary_rules = NULL;
    pthread_mutex_lock(&store->mutex);
    int64_t now = traffic_now_ns();
    size_t matched = 0U, emitted = 0U;
    size_t active = 0U;
    uint64_t rx_total = 0U, tx_total = 0U;
    double rx_bps = 0.0, tx_bps = 0.0;
    for (size_t index = 0U; index < store->count; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        if (entry->active) {
            ++active;
            rx_bps += entry->rx_bps;
            tx_bps += entry->tx_bps;
        }
        rx_total += entry->rx_total;
        tx_total += entry->tx_total;
        if (traffic_entry_matches(entry, &filter)) ++matched;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append_format(&json,
        "{\"updated_ts_ns\":%" PRId64 ",\"summary\":{"
        "\"active_connections\":%zu,\"rx_bps\":%.3f,\"tx_bps\":%.3f,"
        "\"rx_total\":%" PRIu64 ",\"tx_total\":%" PRIu64 ","
        "\"history_limit\":%zu,\"history_path\":\"\","
        "\"history_persisted\":false,\"persist_error\":\"\"},"
        "\"connections\":[",
        now, active, rx_bps, tx_bps, rx_total, tx_total, store->limit);
    size_t skipped = 0U;
    for (size_t index = 0U; okay && index < store->count; ++index) {
        const ch_traffic_entry *entry = &store->entries[index];
        if (!traffic_entry_matches(entry, &filter)) continue;
        if (skipped < filter.offset) {
            ++skipped;
            continue;
        }
        if (emitted >= filter.limit) break;
        if (emitted > 0U) okay = ch_json_append(&json, ",");
        if (okay) okay = traffic_append_connection(&json, entry, now);
        ++emitted;
    }
    if (okay) okay = ch_json_append_format(
        &json, "],\"total\":%zu,\"temporary_rules\":", matched);
    if (okay) okay = ch_json_append(&json, temporary_rules_json);
    if (okay) okay = ch_json_append(&json, ",");
    if (okay) okay = traffic_append_profiles(&json, config, active_profile);
    if (okay) okay = ch_json_append(&json, ",");
    if (okay) okay = traffic_append_quick_filters(&json, store);
    if (okay) okay = ch_json_append(&json, ",");
    if (okay) okay = traffic_append_rule_hits(&json, store);
    if (okay) okay = ch_json_append(&json, ",");
    if (okay) okay = traffic_append_blocks(&json, store);
    if (okay) okay = ch_json_append(&json, ",");
    if (okay) okay = traffic_append_cleanup_suggestions(
        &json, store, config, active_profile);
    if (okay) okay = ch_json_append(&json,
        ",\"rule_suggestions\":[],"
        "\"breakdowns\":{\"profiles\":[],\"chains\":[],"
        "\"rules\":[],\"actions\":[],\"networks\":[]}}");
    pthread_mutex_unlock(&store->mutex);
    traffic_filter_clear(&filter);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode traffic snapshot");
    }
    return result;
}

static char *traffic_trimmed_copy(const char *value) {
    if (value == NULL) return ch_strdup("");
    const char *start = value;
    while (*start != '\0' && isspace((unsigned char)*start)) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    size_t length = (size_t)(end - start);
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    memcpy(copy, start, length);
    copy[length] = '\0';
    return copy;
}

static char *traffic_request_string(const ch_json_value *request,
                                    const char *key) {
    return traffic_trimmed_copy(ch_json_string_value(
        ch_json_object_get(request, key)));
}

static char *traffic_rule_host(const char *value) {
    char *host = traffic_trimmed_copy(value);
    if (host == NULL) return NULL;
    size_t length = strlen(host);
    if (length >= 2U && host[0] == '[' && host[length - 1U] == ']') {
        memmove(host, host + 1U, length - 2U);
        host[length - 2U] = '\0';
        length -= 2U;
    }
    while (length > 0U && host[length - 1U] == '.') {
        host[--length] = '\0';
    }
    for (size_t index = 0U; index < length; ++index) {
        host[index] = (char)tolower((unsigned char)host[index]);
    }
    return host;
}

static int traffic_host_ip(const char *host, char normalized[INET6_ADDRSTRLEN],
                           int *bits) {
    struct in_addr ipv4;
    if (inet_pton(AF_INET, host, &ipv4) == 1) {
        *bits = 32;
        return inet_ntop(AF_INET, &ipv4, normalized, INET6_ADDRSTRLEN) != NULL;
    }
    struct in6_addr ipv6;
    if (inet_pton(AF_INET6, host, &ipv6) == 1) {
        *bits = 128;
        return inet_ntop(AF_INET6, &ipv6, normalized, INET6_ADDRSTRLEN) != NULL;
    }
    return 0;
}

static char *traffic_domain_suffix(const char *host) {
    const char *last = strrchr(host, '.');
    if (last == NULL || last == host) return ch_strdup(host);
    const char *previous = last - 1U;
    while (previous > host && previous[-1] != '.') --previous;
    const char *candidate = previous;
    static const char *broad[] = {
        "co.uk", "com.au", "co.jp", "com.br", "com.cn", "com.sg",
        "co.nz"
    };
    int is_broad = 0;
    for (size_t index = 0U; index < sizeof(broad) / sizeof(broad[0]);
         ++index) {
        if (strcmp(candidate, broad[index]) == 0) {
            is_broad = 1;
            break;
        }
    }
    if (is_broad && candidate > host) {
        const char *third = candidate - 1U;
        while (third > host && third[-1] != '.') --third;
        candidate = third;
    }
    return ch_strdup(candidate);
}

static char *traffic_rule_name(const char *family, const char *host) {
    size_t capacity = strlen(family) + strlen(host) + 2U;
    char *name = malloc(capacity);
    if (name == NULL) return NULL;
    size_t written = 0U;
    for (const char *cursor = family; *cursor != '\0'; ++cursor) {
        name[written++] = (char)tolower((unsigned char)*cursor);
    }
    name[written++] = '-';
    int dash = 0;
    for (const char *cursor = host; *cursor != '\0'; ++cursor) {
        unsigned char value = (unsigned char)*cursor;
        if (isalnum(value)) {
            name[written++] = (char)tolower(value);
            dash = 0;
        } else if (!dash && written > 0U) {
            name[written++] = '-';
            dash = 1;
        }
    }
    while (written > 0U && name[written - 1U] == '-') --written;
    name[written] = '\0';
    return name;
}

static int traffic_config_name_used(const ch_config_table *profile,
                                    const char *name) {
    const ch_config_array *rules = ch_config_table_get_array(profile, "rule");
    size_t count = ch_config_array_count(rules);
    for (size_t index = 0U; index < count; ++index) {
        char *candidate = NULL;
        ch_error ignored;
        const ch_config_table *rule = ch_config_array_get_table(rules, index);
        int used = rule != NULL && ch_config_table_get_string(
            rule, "name", &candidate, &ignored) == CH_OK &&
            strcmp(candidate, name) == 0;
        free(candidate);
        if (used) return 1;
    }
    return 0;
}

static char *traffic_unique_rule_name(const ch_config_table *profile,
                                      const char *base) {
    if (!traffic_config_name_used(profile, base)) return ch_strdup(base);
    for (unsigned int suffix = 2U; suffix < UINT_MAX; ++suffix) {
        int count = snprintf(NULL, 0, "%s-%u", base, suffix);
        if (count < 0) return NULL;
        char *candidate = malloc((size_t)count + 1U);
        if (candidate == NULL) return NULL;
        (void)snprintf(candidate, (size_t)count + 1U, "%s-%u", base,
                       suffix);
        if (!traffic_config_name_used(profile, candidate)) return candidate;
        free(candidate);
    }
    return NULL;
}

static char *traffic_default_chain(const ch_config_table *profile) {
    const ch_config_table *listen = ch_config_table_get_table(profile,
                                                               "listen");
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    char *chain = NULL;
    ch_error ignored;
    if (tun != NULL && ch_config_table_get_string(
            tun, "chain", &chain, &ignored) == CH_OK && chain[0] != '\0') {
        return chain;
    }
    free(chain);
    const ch_config_array *chains = ch_config_table_get_array(profile,
                                                               "chain");
    const ch_config_table *first = ch_config_array_get_table(chains, 0U);
    if (first != NULL && ch_config_table_get_string(
            first, "name", &chain, &ignored) == CH_OK) return chain;
    free(chain);
    return NULL;
}

static char *traffic_rule_action(const ch_traffic_connection *connection,
                                 const ch_config_table *profile,
                                 const char *requested,
                                 const char **family,
                                 ch_error *error) {
    char *lower = traffic_trimmed_copy(requested);
    if (lower == NULL) return NULL;
    for (char *cursor = lower; *cursor != '\0'; ++cursor) {
        *cursor = (char)tolower((unsigned char)*cursor);
    }
    if (lower[0] == '\0' || strcmp(lower, "allow") == 0) {
        free(lower);
        if (strcasecmp(connection->rule_action, "direct") == 0) {
            *family = "allow";
            return ch_strdup("direct");
        }
        if (connection->group_name[0] != '\0') {
            *family = "allow";
            size_t length = strlen(connection->group_name) + 7U;
            char *action = malloc(length);
            if (action != NULL) (void)snprintf(action, length, "group:%s",
                                                connection->group_name);
            return action;
        }
        char *chain = connection->chain_name[0] == '\0' ?
            traffic_default_chain(profile) : ch_strdup(connection->chain_name);
        if (chain == NULL || chain[0] == '\0') {
            free(chain);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile has no chain for allow rule");
            return NULL;
        }
        size_t length = strlen(chain) + 7U;
        char *action = malloc(length);
        if (action != NULL) (void)snprintf(action, length, "chain:%s", chain);
        free(chain);
        *family = "allow";
        return action;
    }
    if (strcmp(lower, "direct") == 0) *family = "direct";
    else if (strcmp(lower, "block") == 0 || strcmp(lower, "reject") == 0)
        *family = "block";
    else if (strncmp(lower, "chain:", 6U) == 0 && lower[6] != '\0')
        *family = "proxy";
    else if (strncmp(lower, "group:", 6U) == 0 && lower[6] != '\0')
        *family = "proxy";
    else {
        free(lower);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
            "action must be allow, direct, block, reject, chain:<name>, or group:<name>");
        return NULL;
    }
    return lower;
}

char *ch_traffic_rule_request_json(ch_traffic_store *store,
                                   const ch_config *config,
                                   const char *active_profile,
                                   const char *request_json,
                                   ch_error *error) {
    ch_error_clear(error);
    if (store == NULL || config == NULL || request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic store, config, and request are required");
        return NULL;
    }
    ch_json_value *request = ch_json_parse(request_json,
                                           strlen(request_json), error);
    if (request == NULL) return NULL;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule request must be a JSON object");
        return NULL;
    }
    char *conn_id = traffic_request_string(request, "conn_id");
    char *profile_name = traffic_request_string(request, "profile");
    char *requested_name = traffic_request_string(request, "name");
    char *requested_action = traffic_request_string(request, "action");
    char *scope = traffic_request_string(request, "scope");
    ch_json_value_destroy(request);
    if (conn_id == NULL || profile_name == NULL || requested_name == NULL ||
        requested_action == NULL || scope == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule request");
        goto failure;
    }
    ch_traffic_connection connection;
    if (ch_traffic_connection_copy(store, conn_id, &connection, error) !=
        CH_OK) goto failure;
    if (profile_name[0] == '\0') {
        free(profile_name);
        profile_name = ch_strdup(connection.profile[0] == '\0' ?
            active_profile : connection.profile);
    }
    if (profile_name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule profile");
        ch_traffic_connection_clear(&connection);
        goto failure;
    }
    const ch_config_table *profile = ch_config_profile_named(config,
                                                              profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name);
        ch_traffic_connection_clear(&connection);
        goto failure;
    }
    char *host = traffic_rule_host(connection.target_host);
    if (host == NULL || host[0] == '\0') {
        free(host);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "connection has no ruleable host");
        ch_traffic_connection_clear(&connection);
        goto failure;
    }
    const char *family = "rule";
    char *action = traffic_rule_action(&connection, profile, requested_action,
                                       &family, error);
    ch_traffic_connection_clear(&connection);
    if (action == NULL) {
        free(host);
        goto failure;
    }
    char normalized[INET6_ADDRSTRLEN];
    int bits = 0;
    int is_ip = traffic_host_ip(host, normalized, &bits);
    if (scope[0] == '\0' || strcasecmp(scope, "auto") == 0) {
        free(scope);
        scope = ch_strdup(is_ip ? "cidr" : "exact_host");
    }
    char *match = NULL;
    const char *match_key = NULL;
    if (scope != NULL && strcasecmp(scope, "exact_host") == 0 && !is_ip) {
        match_key = "domains";
        match = ch_strdup(host);
    } else if (scope != NULL &&
               strcasecmp(scope, "domain_suffix") == 0 && !is_ip) {
        match_key = "domain_suffixes";
        match = traffic_domain_suffix(host);
    } else if (scope != NULL && strcasecmp(scope, "cidr") == 0 && is_ip) {
        match_key = "cidrs";
        int length = snprintf(NULL, 0, "%s/%d", normalized, bits);
        if (length >= 0) {
            match = malloc((size_t)length + 1U);
            if (match != NULL) (void)snprintf(match, (size_t)length + 1U,
                                               "%s/%d", normalized, bits);
        }
    } else {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "scope is incompatible with the connection host");
    }
    char *base_name = requested_name[0] == '\0' ?
        traffic_rule_name(family, host) : ch_strdup(requested_name);
    char *unique_name = base_name == NULL ? NULL :
        traffic_unique_rule_name(profile, base_name);
    free(base_name);
    free(host);
    if (scope == NULL || match == NULL || unique_name == NULL) {
        free(action);
        free(match);
        free(unique_name);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "build connection rule");
        }
        goto failure;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append(&json, ",\"position\":\"append\",\"rule\":{\"name\":") &&
        ch_json_append_string(&json, unique_name) &&
        ch_json_append(&json, ",\"action\":") &&
        ch_json_append_string(&json, action) &&
        ch_json_append(&json, ",\"") && ch_json_append(&json, match_key) &&
        ch_json_append(&json, "\":[") && ch_json_append_string(&json, match) &&
        ch_json_append(&json, "]}}");
    free(action);
    free(match);
    free(unique_name);
    free(conn_id);
    free(profile_name);
    free(requested_name);
    free(requested_action);
    free(scope);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode connection rule request");
    }
    return result;

failure:
    free(conn_id);
    free(profile_name);
    free(requested_name);
    free(requested_action);
    free(scope);
    return NULL;
}

char *ch_traffic_cleanup_request_json(ch_traffic_store *store,
                                      const ch_config *config,
                                      const char *active_profile,
                                      const char *request_json,
                                      ch_error *error) {
    ch_error_clear(error);
    if (store == NULL || config == NULL || request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic cleanup inputs are required");
        return NULL;
    }
    ch_json_value *request = ch_json_parse(request_json,
                                           strlen(request_json), error);
    if (request == NULL) return NULL;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "cleanup request must be a JSON object");
        return NULL;
    }
    char *profile_name = traffic_request_string(request, "profile");
    char *kind = traffic_request_string(request, "kind");
    char *rule_name = traffic_request_string(request, "rule_name");
    char *target_name = traffic_request_string(request, "target_rule_name");
    char *operation = traffic_request_string(request, "operation");
    ch_json_value_destroy(request);
    if (profile_name == NULL || kind == NULL || rule_name == NULL ||
        target_name == NULL || operation == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy cleanup request");
        goto cleanup_failure;
    }
    if (profile_name[0] == '\0') {
        free(profile_name);
        profile_name = ch_strdup(active_profile == NULL ? "" :
                                                           active_profile);
    }
    if (kind[0] == '\0' || rule_name[0] == '\0' || target_name[0] == '\0' ||
        (strcmp(operation, "delete_rule") != 0 &&
         strcmp(operation, "move_rule_to_end") != 0)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid cleanup request");
        goto cleanup_failure;
    }
    const ch_config_table *profile = ch_config_profile_named(config,
                                                              profile_name);
    const ch_config_array *rules = ch_config_table_get_array(profile, "rule");
    size_t count = ch_config_array_count(rules);
    size_t target_index = count;
    for (size_t index = 0U; index < count; ++index) {
        char *name = traffic_config_string(
            ch_config_array_get_table(rules, index), "name");
        int matches = name != NULL && strcmp(name, target_name) == 0;
        free(name);
        if (matches) {
            target_index = index;
            break;
        }
    }
    if (profile == NULL || target_index == count ||
        strcmp(rule_name, target_name) != 0) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "cleanup target rule not found");
        goto cleanup_failure;
    }
    const ch_config_table *target = ch_config_array_get_table(rules,
                                                               target_index);
    pthread_mutex_lock(&store->mutex);
    int live = 0;
    if (strcmp(kind, "broad_match") == 0 &&
        strcmp(operation, "move_rule_to_end") == 0) {
        live = target_index == 0U && !traffic_rule_has_matchers(target);
    } else if (strcmp(kind, "unused_in_history") == 0 &&
               strcmp(operation, "delete_rule") == 0) {
        live = !(target_index == 0U && !traffic_rule_has_matchers(target)) &&
            !traffic_rule_was_hit(store, profile_name, target_name);
    }
    pthread_mutex_unlock(&store->mutex);
    if (!live) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "cleanup suggestion is stale");
        goto cleanup_failure;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append(&json, ",\"rules\":[");
    size_t written = 0U;
    for (size_t index = 0U; okay && index < count; ++index) {
        if (index == target_index) continue;
        char *rule_json = NULL;
        if (ch_config_table_json(ch_config_array_get_table(rules, index),
                                 &rule_json, error) != CH_OK) {
            okay = 0;
            break;
        }
        if (written > 0U) okay = ch_json_append(&json, ",");
        if (okay) okay = ch_json_append(&json, rule_json);
        free(rule_json);
        ++written;
    }
    if (okay && strcmp(operation, "move_rule_to_end") == 0) {
        char *rule_json = NULL;
        if (ch_config_table_json(target, &rule_json, error) != CH_OK) {
            okay = 0;
        } else {
            if (written > 0U) okay = ch_json_append(&json, ",");
            if (okay) okay = ch_json_append(&json, rule_json);
            free(rule_json);
        }
    }
    if (okay) okay = ch_json_append(&json, "]}");
    free(profile_name);
    free(kind);
    free(rule_name);
    free(target_name);
    free(operation);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode cleanup mutation");
    }
    return result;

cleanup_failure:
    free(profile_name);
    free(kind);
    free(rule_name);
    free(target_name);
    free(operation);
    return NULL;
}
