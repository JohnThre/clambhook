#include "policy.h"

#include <arpa/inet.h>
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <openssl/err.h>
#include <openssl/ssl.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/protocol.h"
#include "conditioner.h"
#include "internal.h"

#define CH_POLICY_DEFAULT_INTERVAL_NS INT64_C(30000000000)
#define CH_POLICY_DEFAULT_TIMEOUT_NS INT64_C(5000000000)
#define CH_POLICY_SMART_MIN_DELTA_NS INT64_C(50000000)
#define CH_POLICY_MAX_RESPONSE_LINE 1024U

typedef struct ch_policy_chain_state {
    char *name;
    const ch_config_table *config;
    int udp_capable;
    char *udp_error;
    int has_result;
    ch_policy_probe_result result;
} ch_policy_chain_state;

typedef struct ch_policy_group_state {
    char *name;
    char *type;
    ch_policy_chain_state *chains;
    size_t chain_count;
    char *selected_chain;
    char *test_url;
    char *interval_label;
    char *timeout_label;
    int64_t interval_ns;
    int64_t timeout_ns;
    int64_t next_probe_ns;
    int64_t updated_ts_ns;
    int hidden;
    char selection_reason[32];
} ch_policy_group_state;

struct ch_policy_manager {
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    pthread_t thread;
    int thread_started;
    int stop_requested;
    ch_policy_group_state *groups;
    size_t group_count;
    ch_policy_probe_callback probe;
    void *probe_context;
    ch_conditioner_config conditioner;
};

typedef struct ch_policy_url {
    char *host;
    char *target;
    char *host_header;
    char *path;
    int tls;
} ch_policy_url;

typedef struct ch_policy_probe_task {
    pthread_t thread;
    ch_policy_probe_callback probe;
    const ch_config_table *chain;
    const char *test_url;
    unsigned int timeout_ms;
    ch_policy_probe_result *result;
    void *probe_context;
    int started;
} ch_policy_probe_task;

static int64_t ch_policy_clock_ns(clockid_t clock_id) {
    struct timespec value;
    if (clock_gettime(clock_id, &value) != 0) return 0;
    return (int64_t)value.tv_sec * INT64_C(1000000000) +
        (int64_t)value.tv_nsec;
}

static char *ch_policy_optional_string(const ch_config_table *table,
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

static int ch_policy_optional_bool(const ch_config_table *table,
                                   const char *key) {
    bool value = false;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_bool(table, key, &value, &ignored);
    }
    return value ? 1 : 0;
}

static const ch_config_table *ch_policy_named_table(
    const ch_config_array *array, const char *name) {
    size_t count = ch_config_array_count(array);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        char *candidate = ch_policy_optional_string(table, "name");
        int matches = candidate != NULL && strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return table;
    }
    return NULL;
}

static void ch_policy_group_clear(ch_policy_group_state *group) {
    if (group == NULL) return;
    for (size_t index = 0U; index < group->chain_count; ++index) {
        free(group->chains[index].name);
        free(group->chains[index].udp_error);
    }
    free(group->chains);
    free(group->name);
    free(group->type);
    free(group->selected_chain);
    free(group->test_url);
    free(group->interval_label);
    free(group->timeout_label);
    memset(group, 0, sizeof(*group));
}

static int ch_policy_type_uses_health(const char *type) {
    return strcasecmp(type, "select") != 0;
}

static const char *ch_policy_selection_mode(const char *type) {
    if (strcasecmp(type, "select") == 0) return "manual";
    if (strcasecmp(type, "fallback") == 0) return "fallback";
    if (strcasecmp(type, "load-balance") == 0) return "load-balance";
    if (strcasecmp(type, "smart") == 0) return "smart";
    return "latency";
}

static const char *ch_policy_initial_reason(const char *type) {
    if (strcasecmp(type, "select") == 0) return "manual";
    if (strcasecmp(type, "fallback") == 0) return "first_healthy";
    if (strcasecmp(type, "load-balance") == 0) return "stable_hash";
    if (strcasecmp(type, "smart") == 0) return "sticky_healthy";
    return "lowest_latency";
}

static ch_status ch_policy_copy_group(
    ch_policy_group_state *out_group, const ch_config_table *group,
    const ch_config_array *profile_chains, ch_error *error) {
    memset(out_group, 0, sizeof(*out_group));
    out_group->name = ch_policy_optional_string(group, "name");
    out_group->type = ch_policy_optional_string(group, "type");
    out_group->selected_chain = ch_policy_optional_string(group, "selected");
    out_group->test_url = ch_policy_optional_string(group, "test_url");
    out_group->interval_label = ch_policy_optional_string(group, "interval");
    out_group->timeout_label = ch_policy_optional_string(group, "timeout");
    if (out_group->name == NULL || out_group->type == NULL ||
        out_group->selected_chain == NULL || out_group->test_url == NULL ||
        out_group->interval_label == NULL || out_group->timeout_label == NULL) {
        ch_policy_group_clear(out_group);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy policy group configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (out_group->test_url[0] == '\0') {
        free(out_group->test_url);
        out_group->test_url = ch_strdup(
            "https://www.gstatic.com/generate_204");
    }
    if (out_group->interval_label[0] == '\0') {
        free(out_group->interval_label);
        out_group->interval_label = ch_strdup("30s");
    }
    if (out_group->timeout_label[0] == '\0') {
        free(out_group->timeout_label);
        out_group->timeout_label = ch_strdup("5s");
    }
    if (out_group->test_url == NULL || out_group->interval_label == NULL ||
        out_group->timeout_label == NULL) {
        ch_policy_group_clear(out_group);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy policy group defaults");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    out_group->interval_ns = CH_POLICY_DEFAULT_INTERVAL_NS;
    out_group->timeout_ns = CH_POLICY_DEFAULT_TIMEOUT_NS;
    if (ch_config_parse_duration_ns(out_group->interval_label,
                                    &out_group->interval_ns, error) != CH_OK ||
        ch_config_parse_duration_ns(out_group->timeout_label,
                                    &out_group->timeout_ns, error) != CH_OK) {
        ch_policy_group_clear(out_group);
        return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
    }
    out_group->hidden = ch_policy_optional_bool(group, "hidden");
    (void)snprintf(out_group->selection_reason,
                   sizeof(out_group->selection_reason), "%s",
                   ch_policy_initial_reason(out_group->type));

    const ch_config_array *members = ch_config_table_get_array(group, "chains");
    out_group->chain_count = ch_config_array_count(members);
    out_group->chains = calloc(out_group->chain_count,
                               sizeof(*out_group->chains));
    if (out_group->chain_count == 0U || out_group->chains == NULL) {
        int empty = out_group->chain_count == 0U;
        ch_policy_group_clear(out_group);
        ch_error_set(error, empty ?
                     CH_ERROR_INVALID_ARGUMENT : CH_ERROR_OUT_OF_MEMORY,
                     "policy group requires member chains");
        return empty ? CH_ERROR_INVALID_ARGUMENT : CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < out_group->chain_count; ++index) {
        ch_policy_chain_state *state = &out_group->chains[index];
        if (ch_config_array_get_string(members, index, &state->name, error) !=
            CH_OK) {
            ch_policy_group_clear(out_group);
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
        state->config = ch_policy_named_table(profile_chains, state->name);
        if (state->config == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "policy chain %s not found", state->name);
            ch_policy_group_clear(out_group);
            return CH_ERROR_NOT_FOUND;
        }
        ch_error packet_error;
        if (ch_protocol_chain_supports_packet(state->config, &packet_error) ==
            CH_OK) {
            state->udp_capable = 1;
        } else {
            state->udp_error = ch_strdup(packet_error.message);
            if (state->udp_error == NULL) {
                ch_policy_group_clear(out_group);
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy policy UDP support error");
                return CH_ERROR_OUT_OF_MEMORY;
            }
        }
    }
    if (out_group->selected_chain[0] == '\0') {
        free(out_group->selected_chain);
        out_group->selected_chain = ch_strdup(out_group->chains[0].name);
        if (out_group->selected_chain == NULL) {
            ch_policy_group_clear(out_group);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy initial policy selection");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    return CH_OK;
}

static int ch_policy_chain_eligible(const ch_policy_chain_state *chain,
                                    int udp_only) {
    return !udp_only || chain->udp_capable;
}

static size_t ch_policy_first_eligible(const ch_policy_group_state *group,
                                       int udp_only) {
    for (size_t index = 0U; index < group->chain_count; ++index) {
        if (ch_policy_chain_eligible(&group->chains[index], udp_only)) {
            return index;
        }
    }
    return SIZE_MAX;
}

static size_t ch_policy_chain_index(const ch_policy_group_state *group,
                                    const char *name) {
    for (size_t index = 0U; index < group->chain_count; ++index) {
        if (strcmp(group->chains[index].name, name) == 0) return index;
    }
    return SIZE_MAX;
}

static size_t ch_policy_lowest_latency(const ch_policy_group_state *group,
                                       int udp_only) {
    size_t best = SIZE_MAX;
    int64_t latency = 0;
    for (size_t index = 0U; index < group->chain_count; ++index) {
        const ch_policy_chain_state *chain = &group->chains[index];
        if (!ch_policy_chain_eligible(chain, udp_only) || !chain->has_result ||
            !chain->result.healthy) {
            continue;
        }
        if (best == SIZE_MAX || chain->result.latency_ns < latency) {
            best = index;
            latency = chain->result.latency_ns;
        }
    }
    return best;
}

static uint32_t ch_policy_fnv1a(const char *source, const char *network,
                                const char *target) {
    uint32_t hash = UINT32_C(2166136261);
    const char *values[] = {source, "|", network, "|", target};
    for (size_t part = 0U; part < sizeof(values) / sizeof(values[0]); ++part) {
        const unsigned char *cursor = (const unsigned char *)values[part];
        while (*cursor != '\0') {
            unsigned char byte = *cursor++;
            if (part == 2U && byte >= (unsigned char)'A' &&
                byte <= (unsigned char)'Z') {
                byte = (unsigned char)(byte - (unsigned char)'A' +
                                       (unsigned char)'a');
            }
            hash ^= (uint32_t)byte;
            hash *= UINT32_C(16777619);
        }
    }
    return hash;
}

static size_t ch_policy_choose_locked(ch_policy_group_state *group,
                                      const char *network,
                                      const char *target,
                                      const char *source,
                                      const char **out_reason) {
    int udp_only = strcasecmp(network, "udp") == 0;
    if (strcasecmp(group->type, "select") == 0) {
        *out_reason = "manual";
        return ch_policy_chain_index(group, group->selected_chain);
    }
    if (strcasecmp(group->type, "fallback") == 0) {
        for (size_t index = 0U; index < group->chain_count; ++index) {
            ch_policy_chain_state *chain = &group->chains[index];
            if (ch_policy_chain_eligible(chain, udp_only) &&
                chain->has_result && chain->result.healthy) {
                *out_reason = "first_healthy";
                return index;
            }
        }
        *out_reason = "no_healthy_fallback";
        return ch_policy_first_eligible(group, udp_only);
    }
    if (strcasecmp(group->type, "load-balance") == 0) {
        size_t healthy_count = 0U;
        for (size_t index = 0U; index < group->chain_count; ++index) {
            ch_policy_chain_state *chain = &group->chains[index];
            if (ch_policy_chain_eligible(chain, udp_only) &&
                chain->has_result && chain->result.healthy) {
                ++healthy_count;
            }
        }
        if (healthy_count == 0U) {
            *out_reason = "no_healthy_fallback";
            return ch_policy_first_eligible(group, udp_only);
        }
        size_t wanted = (size_t)(ch_policy_fnv1a(source, network, target) %
                                 (uint32_t)healthy_count);
        for (size_t index = 0U; index < group->chain_count; ++index) {
            ch_policy_chain_state *chain = &group->chains[index];
            if (ch_policy_chain_eligible(chain, udp_only) &&
                chain->has_result && chain->result.healthy) {
                if (wanted == 0U) {
                    *out_reason = "stable_hash";
                    return index;
                }
                --wanted;
            }
        }
    }
    size_t best = ch_policy_lowest_latency(group, udp_only);
    if (best == SIZE_MAX) {
        *out_reason = "no_healthy_fallback";
        return ch_policy_first_eligible(group, udp_only);
    }
    if (strcasecmp(group->type, "smart") != 0) {
        *out_reason = "lowest_latency";
        return best;
    }
    size_t current = ch_policy_chain_index(group, group->selected_chain);
    if (current == SIZE_MAX ||
        !ch_policy_chain_eligible(&group->chains[current], udp_only) ||
        !group->chains[current].has_result ||
        !group->chains[current].result.healthy) {
        *out_reason = "lowest_latency";
        return best;
    }
    if (current == best) {
        *out_reason = "sticky_healthy";
        return current;
    }
    int64_t current_latency = group->chains[current].result.latency_ns;
    int64_t delta = current_latency - group->chains[best].result.latency_ns;
    int64_t threshold = current_latency / 5;
    if (threshold < CH_POLICY_SMART_MIN_DELTA_NS) {
        threshold = CH_POLICY_SMART_MIN_DELTA_NS;
    }
    if (delta >= threshold) {
        *out_reason = "better_latency";
        return best;
    }
    *out_reason = "sticky_healthy";
    return current;
}

static void ch_policy_set_selection_locked(ch_policy_group_state *group,
                                           size_t selected,
                                           const char *reason) {
    if (selected != SIZE_MAX) {
        char *copy = ch_strdup(group->chains[selected].name);
        if (copy != NULL) {
            free(group->selected_chain);
            group->selected_chain = copy;
        }
    }
    (void)snprintf(group->selection_reason,
                   sizeof(group->selection_reason), "%s", reason);
    group->updated_ts_ns = ch_policy_clock_ns(CLOCK_REALTIME);
}

ch_policy_manager *ch_policy_manager_create(
    const ch_config *config, const char *profile_name,
    const ch_policy_options *options, ch_error *error) {
    ch_error_clear(error);
    if (config == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy configuration is required");
        return NULL;
    }
    const ch_config_table *profile = profile_name == NULL ||
            profile_name[0] == '\0' ? ch_config_active_profile(config) :
        ch_config_profile_named(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "policy profile not found");
        return NULL;
    }
    ch_policy_manager *manager = calloc(1U, sizeof(*manager));
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate policy manager");
        return NULL;
    }
    if (pthread_mutex_init(&manager->mutex, NULL) != 0) {
        free(manager);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize policy manager mutex");
        return NULL;
    }
    if (pthread_cond_init(&manager->condition, NULL) != 0) {
        (void)pthread_mutex_destroy(&manager->mutex);
        free(manager);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize policy manager condition");
        return NULL;
    }
    manager->probe = options == NULL ? NULL : options->probe;
    manager->probe_context = options == NULL ? NULL : options->probe_context;
    ch_conditioner_config_load(profile, &manager->conditioner);
    const ch_config_array *groups = ch_config_table_get_array(profile,
                                                               "policy_group");
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    manager->group_count = ch_config_array_count(groups);
    manager->groups = calloc(manager->group_count, sizeof(*manager->groups));
    if (manager->group_count > 0U && manager->groups == NULL) {
        ch_policy_manager_destroy(manager);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate policy groups");
        return NULL;
    }
    for (size_t index = 0U; index < manager->group_count; ++index) {
        const ch_config_table *group = ch_config_array_get_table(groups, index);
        ch_status status = ch_policy_copy_group(&manager->groups[index], group,
                                                chains, error);
        if (status != CH_OK) {
            ch_policy_manager_destroy(manager);
            return NULL;
        }
    }
    return manager;
}

ch_status ch_policy_manager_select(
    ch_policy_manager *manager, const char *group_name, const char *network,
    const char *target, const char *source, char **out_chain_name,
    ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || group_name == NULL || network == NULL ||
        target == NULL || source == NULL || out_chain_name == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy manager, route context, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_chain_name = NULL;
    (void)pthread_mutex_lock(&manager->mutex);
    ch_policy_group_state *group = NULL;
    for (size_t index = 0U; index < manager->group_count; ++index) {
        if (strcmp(manager->groups[index].name, group_name) == 0) {
            group = &manager->groups[index];
            break;
        }
    }
    if (group == NULL) {
        (void)pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "policy group %s not found", group_name);
        return CH_ERROR_NOT_FOUND;
    }
    const char *reason = "no_member";
    size_t selected = ch_policy_choose_locked(group, network, target, source,
                                              &reason);
    if (selected == SIZE_MAX) {
        (void)pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "policy group %s has no %s-capable member chains",
                     group_name, strcasecmp(network, "udp") == 0 ? "UDP" :
                                                                  "route");
        return CH_ERROR_UNSUPPORTED;
    }
    if (strcasecmp(network, "udp") == 0 &&
        !group->chains[selected].udp_capable) {
        const char *detail = group->chains[selected].udp_error;
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "policy group %s selected chain %s is not UDP-capable%s%s",
                     group_name, group->chains[selected].name,
                     detail == NULL || detail[0] == '\0' ? "" : ": ",
                     detail == NULL ? "" : detail);
        (void)pthread_mutex_unlock(&manager->mutex);
        return CH_ERROR_UNSUPPORTED;
    }
    *out_chain_name = ch_strdup(group->chains[selected].name);
    if (*out_chain_name == NULL) {
        (void)pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy selected policy chain");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_policy_set_selection_locked(group, selected, reason);
    (void)pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

static void ch_policy_url_clear(ch_policy_url *url) {
    free(url->host);
    free(url->target);
    free(url->host_header);
    free(url->path);
    memset(url, 0, sizeof(*url));
}

static char *ch_policy_copy_range(const char *start, const char *end) {
    size_t length = (size_t)(end - start);
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    memcpy(copy, start, length);
    copy[length] = '\0';
    return copy;
}

static ch_status ch_policy_parse_url(const char *raw, ch_policy_url *out,
                                     ch_error *error) {
    memset(out, 0, sizeof(*out));
    const char *authority = NULL;
    const char *default_port = NULL;
    if (strncmp(raw, "https://", 8U) == 0) {
        out->tls = 1;
        authority = raw + 8U;
        default_port = "443";
    } else if (strncmp(raw, "http://", 7U) == 0) {
        authority = raw + 7U;
        default_port = "80";
    } else {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy test URL must use http or https");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *authority_end = strpbrk(authority, "/?#");
    if (authority_end == NULL) authority_end = authority + strlen(authority);
    if (authority_end == authority ||
        memchr(authority, '@', (size_t)(authority_end - authority)) != NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy test URL authority is invalid");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = authority;
    const char *host_end = authority_end;
    const char *port_start = NULL;
    if (*host_start == '[') {
        ++host_start;
        host_end = memchr(host_start, ']',
                          (size_t)(authority_end - host_start));
        if (host_end == NULL ||
            (host_end + 1 < authority_end && host_end[1] != ':')) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "policy test URL IPv6 authority is invalid");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        if (host_end + 1 < authority_end) port_start = host_end + 2;
    } else {
        const char *colon = memchr(authority, ':',
                                   (size_t)(authority_end - authority));
        if (colon != NULL) {
            host_end = colon;
            port_start = colon + 1;
            if (memchr(port_start, ':',
                       (size_t)(authority_end - port_start)) != NULL) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "policy test URL IPv6 host must be bracketed");
                return CH_ERROR_INVALID_ARGUMENT;
            }
        }
    }
    if (host_end == host_start ||
        (port_start != NULL && port_start == authority_end)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy test URL host or port is empty");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    out->host = ch_policy_copy_range(host_start, host_end);
    char *port = port_start == NULL ? ch_strdup(default_port) :
        ch_policy_copy_range(port_start, authority_end);
    char *authority_copy = ch_policy_copy_range(authority, authority_end);
    if (out->host == NULL || port == NULL || authority_copy == NULL) {
        free(port);
        free(authority_copy);
        ch_policy_url_clear(out);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy policy test URL");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t target_size = strlen(out->host) + strlen(port) + 4U;
    out->target = malloc(target_size);
    if (out->target != NULL) {
        (void)snprintf(out->target, target_size,
                       strchr(out->host, ':') == NULL ? "%s:%s" : "[%s]:%s",
                       out->host, port);
    }
    out->host_header = authority_copy;
    const char *path_start = authority_end;
    const char *fragment = strchr(path_start, '#');
    const char *path_end = fragment == NULL ? raw + strlen(raw) : fragment;
    if (path_start == path_end) {
        out->path = ch_strdup("/");
    } else if (*path_start == '?') {
        size_t length = (size_t)(path_end - path_start);
        out->path = malloc(length + 2U);
        if (out->path != NULL) {
            out->path[0] = '/';
            memcpy(out->path + 1U, path_start, length);
            out->path[length + 1U] = '\0';
        }
    } else {
        out->path = ch_policy_copy_range(path_start, path_end);
    }
    free(port);
    if (out->target == NULL || out->path == NULL) {
        ch_policy_url_clear(out);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy policy test URL request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static void ch_policy_socket_timeout(int descriptor,
                                     unsigned int milliseconds) {
    struct timeval timeout = {
        .tv_sec = (time_t)(milliseconds / 1000U),
        .tv_usec = (suseconds_t)((milliseconds % 1000U) * 1000U)
    };
    (void)setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
    (void)setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
#ifdef SO_NOSIGPIPE
    int enabled = 1;
    (void)setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &enabled,
                     (socklen_t)sizeof(enabled));
#endif
}

static int ch_policy_send_plain(int descriptor, const char *request,
                                size_t length) {
    while (length > 0U) {
        ssize_t written;
#ifdef MSG_NOSIGNAL
        written = send(descriptor, request, length, MSG_NOSIGNAL);
#else
        written = send(descriptor, request, length, 0);
#endif
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        request += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_policy_receive_status_plain(int descriptor, char *line,
                                          size_t capacity) {
    size_t length = 0U;
    while (length + 1U < capacity) {
        ssize_t received = recv(descriptor, line + length, 1U, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) break;
        ++length;
        if (line[length - 1U] == '\n') break;
    }
    line[length] = '\0';
    return length > 0U;
}

static int ch_policy_send_tls(SSL *ssl, const char *request, size_t length) {
    while (length > 0U) {
        int amount = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int written = SSL_write(ssl, request, amount);
        if (written <= 0) return 0;
        request += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_policy_receive_status_tls(SSL *ssl, char *line,
                                        size_t capacity) {
    size_t length = 0U;
    while (length + 1U < capacity) {
        int received = SSL_read(ssl, line + length, 1);
        if (received <= 0) break;
        ++length;
        if (line[length - 1U] == '\n') break;
    }
    line[length] = '\0';
    return length > 0U;
}

static void ch_policy_result_error(ch_policy_probe_result *result,
                                   const char *message) {
    (void)snprintf(result->error, sizeof(result->error), "%s",
                   message == NULL ? "policy probe failed" : message);
}

static ch_status ch_policy_default_probe(
    const ch_config_table *chain, const char *test_url,
    unsigned int timeout_milliseconds, ch_policy_probe_result *out_result,
    void *context, ch_error *error) {
    ch_error_clear(error);
    memset(out_result, 0, sizeof(*out_result));
    int64_t started_mono = ch_policy_clock_ns(CLOCK_MONOTONIC);
    out_result->last_test_ts_ns = ch_policy_clock_ns(CLOCK_REALTIME);
    ch_policy_url url;
    ch_status status = ch_policy_parse_url(test_url, &url, error);
    if (status != CH_OK) return status;
    int descriptor = -1;
    ch_error dial_error;
    status = ch_protocol_chain_dial_timeout(
        chain, "tcp", url.target, timeout_milliseconds, &descriptor,
        &dial_error);
    if (status != CH_OK) {
        ch_policy_result_error(out_result, dial_error.message);
        goto complete;
    }
    int conditioned = -1;
    status = ch_conditioner_wrap_stream(
        descriptor, context, &conditioned, &dial_error);
    descriptor = conditioned;
    if (status != CH_OK) {
        ch_policy_result_error(out_result, dial_error.message);
        goto complete;
    }
    ch_policy_socket_timeout(descriptor, timeout_milliseconds);
    size_t request_size = strlen(url.path) + strlen(url.host_header) + 96U;
    char *request = malloc(request_size);
    if (request == NULL) {
        (void)close(descriptor);
        ch_policy_url_clear(&url);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate policy HTTP request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(request, request_size,
                   "HEAD %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: "
                   "ClambHook/1\r\nConnection: close\r\n\r\n",
                   url.path, url.host_header);
    char line[CH_POLICY_MAX_RESPONSE_LINE];
    int received = 0;
    if (!url.tls) {
        if (!ch_policy_send_plain(descriptor, request, strlen(request))) {
            ch_policy_result_error(out_result, strerror(errno));
        } else {
            received = ch_policy_receive_status_plain(descriptor, line,
                                                      sizeof(line));
        }
    } else {
        SSL_CTX *ssl_context = SSL_CTX_new(TLS_client_method());
        SSL *ssl = NULL;
        int okay = ssl_context != NULL &&
            SSL_CTX_set_min_proto_version(ssl_context, TLS1_2_VERSION) == 1 &&
            SSL_CTX_set_default_verify_paths(ssl_context) == 1;
        if (okay) {
            SSL_CTX_set_verify(ssl_context, SSL_VERIFY_PEER, NULL);
            ssl = SSL_new(ssl_context);
            okay = ssl != NULL && SSL_set_fd(ssl, descriptor) == 1;
            unsigned char ip[sizeof(struct in6_addr)];
            int is_ip = inet_pton(AF_INET, url.host, ip) == 1 ||
                inet_pton(AF_INET6, url.host, ip) == 1;
            if (okay && is_ip) {
                okay = X509_VERIFY_PARAM_set1_ip_asc(
                    SSL_get0_param(ssl), url.host) == 1;
            } else if (okay) {
                okay = SSL_set1_host(ssl, url.host) == 1 &&
                    SSL_set_tlsext_host_name(ssl, url.host) == 1;
            }
            if (okay) okay = SSL_connect(ssl) == 1;
        }
        if (!okay) {
            char ssl_error[192] = "TLS policy probe failed";
            unsigned long code = ERR_get_error();
            if (code != 0UL) ERR_error_string_n(code, ssl_error,
                                                sizeof(ssl_error));
            ch_policy_result_error(out_result, ssl_error);
        } else if (!ch_policy_send_tls(ssl, request, strlen(request))) {
            ch_policy_result_error(out_result, "send TLS policy probe");
        } else {
            received = ch_policy_receive_status_tls(ssl, line, sizeof(line));
        }
        if (ssl != NULL) {
            (void)SSL_shutdown(ssl);
            SSL_free(ssl);
        }
        SSL_CTX_free(ssl_context);
    }
    free(request);
    (void)shutdown(descriptor, SHUT_RDWR);
    (void)close(descriptor);
    if (received) {
        int status_code = 0;
        if (sscanf(line, "HTTP/%*u.%*u %d", &status_code) == 1 &&
            status_code >= 100 && status_code <= 599) {
            out_result->status_code = status_code;
            out_result->healthy = status_code < 500;
            if (!out_result->healthy) {
                (void)snprintf(out_result->error, sizeof(out_result->error),
                               "probe returned HTTP %d", status_code);
            }
        } else {
            ch_policy_result_error(out_result,
                                   "invalid HTTP policy probe response");
        }
    } else if (out_result->error[0] == '\0') {
        ch_policy_result_error(out_result, "read HTTP policy probe response");
    }

complete:
    out_result->latency_ns = ch_policy_clock_ns(CLOCK_MONOTONIC) - started_mono;
    ch_policy_url_clear(&url);
    return CH_OK;
}

static void ch_policy_run_probe(ch_policy_probe_task *task) {
    ch_error probe_error;
    ch_error_clear(&probe_error);
    ch_status status = task->probe(task->chain, task->test_url,
                                   task->timeout_ms, task->result,
                                   task->probe_context, &probe_error);
    if (task->result->last_test_ts_ns == 0) {
        task->result->last_test_ts_ns = ch_policy_clock_ns(CLOCK_REALTIME);
    }
    if (status != CH_OK && task->result->error[0] == '\0') {
        ch_policy_result_error(task->result, probe_error.message);
    }
}

static void *ch_policy_probe_thread(void *context) {
    ch_policy_run_probe(context);
    return NULL;
}

static ch_status ch_policy_refresh_index(ch_policy_manager *manager,
                                         size_t group_index,
                                         ch_error *error) {
    ch_policy_group_state *group = &manager->groups[group_index];
    if (!ch_policy_type_uses_health(group->type)) {
        (void)pthread_mutex_lock(&manager->mutex);
        group->updated_ts_ns = ch_policy_clock_ns(CLOCK_REALTIME);
        (void)pthread_mutex_unlock(&manager->mutex);
        return CH_OK;
    }
    unsigned int timeout_ms = group->timeout_ns <= 0 ? 1U :
        group->timeout_ns / INT64_C(1000000) > (int64_t)INT_MAX ? INT_MAX :
        (unsigned int)(group->timeout_ns / INT64_C(1000000));
    if (timeout_ms == 0U) timeout_ms = 1U;
    ch_policy_probe_result *results = calloc(group->chain_count,
                                             sizeof(*results));
    if (results == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate policy probe results");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_policy_probe_callback probe = manager->probe == NULL ?
        ch_policy_default_probe : manager->probe;
    void *probe_context = manager->probe == NULL ?
        &manager->conditioner : manager->probe_context;
    ch_policy_probe_task *tasks = calloc(group->chain_count, sizeof(*tasks));
    if (tasks != NULL) {
        for (size_t index = 0U; index < group->chain_count; ++index) {
            tasks[index] = (ch_policy_probe_task){
                .probe = probe,
                .chain = group->chains[index].config,
                .test_url = group->test_url,
                .timeout_ms = timeout_ms,
                .result = &results[index],
                .probe_context = probe_context
            };
            if (pthread_create(&tasks[index].thread, NULL,
                               ch_policy_probe_thread, &tasks[index]) == 0) {
                tasks[index].started = 1;
            } else {
                ch_policy_run_probe(&tasks[index]);
            }
        }
        for (size_t index = 0U; index < group->chain_count; ++index) {
            if (tasks[index].started) {
                (void)pthread_join(tasks[index].thread, NULL);
            }
        }
        free(tasks);
    } else {
        ch_policy_probe_task task = {
            .probe = probe,
            .test_url = group->test_url,
            .timeout_ms = timeout_ms,
            .probe_context = probe_context
        };
        for (size_t index = 0U; index < group->chain_count; ++index) {
            task.chain = group->chains[index].config;
            task.result = &results[index];
            ch_policy_run_probe(&task);
        }
    }
    (void)pthread_mutex_lock(&manager->mutex);
    for (size_t index = 0U; index < group->chain_count; ++index) {
        group->chains[index].result = results[index];
        group->chains[index].has_result = 1;
    }
    const char *reason = "no_member";
    size_t selected = ch_policy_choose_locked(group, "tcp", "", "", &reason);
    ch_policy_set_selection_locked(group, selected, reason);
    group->next_probe_ns = ch_policy_clock_ns(CLOCK_MONOTONIC) +
        group->interval_ns;
    (void)pthread_mutex_unlock(&manager->mutex);
    free(results);
    return CH_OK;
}

ch_status ch_policy_manager_refresh(ch_policy_manager *manager,
                                    const char *group_name,
                                    ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy manager is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (group_name != NULL && group_name[0] != '\0') {
        for (size_t index = 0U; index < manager->group_count; ++index) {
            if (strcmp(manager->groups[index].name, group_name) == 0) {
                return ch_policy_refresh_index(manager, index, error);
            }
        }
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "policy group %s not found", group_name);
        return CH_ERROR_NOT_FOUND;
    }
    for (size_t index = 0U; index < manager->group_count; ++index) {
        ch_status status = ch_policy_refresh_index(manager, index, error);
        if (status != CH_OK) return status;
    }
    return CH_OK;
}

static void ch_policy_wait_until_locked(ch_policy_manager *manager,
                                        int64_t target_mono_ns) {
    int64_t delay = target_mono_ns - ch_policy_clock_ns(CLOCK_MONOTONIC);
    if (delay < 0) delay = 0;
    int64_t target_real = ch_policy_clock_ns(CLOCK_REALTIME) + delay;
    struct timespec deadline = {
        .tv_sec = (time_t)(target_real / INT64_C(1000000000)),
        .tv_nsec = (long)(target_real % INT64_C(1000000000))
    };
    (void)pthread_cond_timedwait(&manager->condition, &manager->mutex,
                                 &deadline);
}

static void *ch_policy_thread_main(void *context) {
    ch_policy_manager *manager = context;
    (void)pthread_mutex_lock(&manager->mutex);
    while (!manager->stop_requested) {
        size_t due = SIZE_MAX;
        int64_t next = INT64_MAX;
        int64_t now = ch_policy_clock_ns(CLOCK_MONOTONIC);
        for (size_t index = 0U; index < manager->group_count; ++index) {
            ch_policy_group_state *group = &manager->groups[index];
            if (!ch_policy_type_uses_health(group->type)) continue;
            if (group->next_probe_ns <= now) {
                due = index;
                group->next_probe_ns = now + group->interval_ns;
                break;
            }
            if (group->next_probe_ns < next) next = group->next_probe_ns;
        }
        if (due != SIZE_MAX) {
            (void)pthread_mutex_unlock(&manager->mutex);
            ch_error ignored;
            (void)ch_policy_refresh_index(manager, due, &ignored);
            (void)pthread_mutex_lock(&manager->mutex);
        } else if (next != INT64_MAX) {
            ch_policy_wait_until_locked(manager, next);
        } else {
            (void)pthread_cond_wait(&manager->condition, &manager->mutex);
        }
    }
    (void)pthread_mutex_unlock(&manager->mutex);
    return NULL;
}

ch_status ch_policy_manager_start(ch_policy_manager *manager, ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy manager is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    (void)pthread_mutex_lock(&manager->mutex);
    if (manager->thread_started) {
        (void)pthread_mutex_unlock(&manager->mutex);
        return CH_OK;
    }
    manager->stop_requested = 0;
    for (size_t index = 0U; index < manager->group_count; ++index) {
        manager->groups[index].next_probe_ns = 0;
    }
    int created = pthread_create(&manager->thread, NULL,
                                 ch_policy_thread_main, manager);
    if (created != 0) {
        (void)pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "start policy probe thread: %s", strerror(created));
        return CH_ERROR_INTERNAL;
    }
    manager->thread_started = 1;
    (void)pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

void ch_policy_manager_stop(ch_policy_manager *manager) {
    if (manager == NULL) return;
    (void)pthread_mutex_lock(&manager->mutex);
    int started = manager->thread_started;
    manager->stop_requested = 1;
    (void)pthread_cond_broadcast(&manager->condition);
    (void)pthread_mutex_unlock(&manager->mutex);
    if (started) (void)pthread_join(manager->thread, NULL);
    (void)pthread_mutex_lock(&manager->mutex);
    manager->thread_started = 0;
    (void)pthread_mutex_unlock(&manager->mutex);
}

static int ch_policy_append_result(ch_json_buffer *json,
                                   const ch_policy_chain_state *chain) {
    int okay = ch_json_append(json, "{\"chain_name\":") &&
        ch_json_append_string(json, chain->name) &&
        ch_json_append_format(json, ",\"healthy\":%s",
                              chain->result.healthy ? "true" : "false");
    if (okay && chain->result.latency_ns != 0) {
        okay = ch_json_append_format(json, ",\"latency_ns\":%" PRId64,
                                     chain->result.latency_ns);
    }
    if (okay && chain->result.status_code != 0) {
        okay = ch_json_append_format(json, ",\"status_code\":%d",
                                     chain->result.status_code);
    }
    if (okay && chain->result.error[0] != '\0') {
        okay = ch_json_append(json, ",\"error\":") &&
            ch_json_append_string(json, chain->result.error);
    }
    if (okay && chain->result.last_test_ts_ns != 0) {
        okay = ch_json_append_format(json, ",\"last_test_ts_ns\":%" PRId64,
                                     chain->result.last_test_ts_ns);
    }
    if (okay) {
        okay = ch_json_append_format(json, ",\"udp_capable\":%s",
                                     chain->udp_capable ? "true" : "false");
    }
    if (okay && chain->udp_error != NULL && chain->udp_error[0] != '\0') {
        okay = ch_json_append(json, ",\"udp_error\":") &&
            ch_json_append_string(json, chain->udp_error);
    }
    return okay && ch_json_append(json, "}");
}

char *ch_policy_manager_snapshot_json(ch_policy_manager *manager,
                                      const char *profile_name,
                                      ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || profile_name == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy manager and profile are required");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    (void)pthread_mutex_lock(&manager->mutex);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append(&json, ",\"groups\":[");
    for (size_t group_index = 0U; okay &&
         group_index < manager->group_count; ++group_index) {
        ch_policy_group_state *group = &manager->groups[group_index];
        okay = (group_index == 0U || ch_json_append(&json, ",")) &&
            ch_json_append(&json, "{\"name\":") &&
            ch_json_append_string(&json, group->name) &&
            ch_json_append(&json, ",\"type\":") &&
            ch_json_append_string(&json, group->type) &&
            ch_json_append(&json, ",\"chains\":[");
        for (size_t chain_index = 0U; okay &&
             chain_index < group->chain_count; ++chain_index) {
            okay = (chain_index == 0U || ch_json_append(&json, ",")) &&
                ch_json_append_string(&json, group->chains[chain_index].name);
        }
        if (okay) {
            okay = ch_json_append(&json, "],\"selected\":") &&
                ch_json_append_string(&json, group->selected_chain) &&
                (!group->hidden || ch_json_append(&json, ",\"hidden\":true")) &&
                ch_json_append(&json, ",\"test_url\":") &&
                ch_json_append_string(&json, group->test_url) &&
                ch_json_append(&json, ",\"interval\":") &&
                ch_json_append_string(&json, group->interval_label) &&
                ch_json_append(&json, ",\"timeout\":") &&
                ch_json_append_string(&json, group->timeout_label) &&
                ch_json_append(&json, ",\"selected_chain\":") &&
                ch_json_append_string(&json, group->selected_chain) &&
                ch_json_append(&json, ",\"selection_mode\":") &&
                ch_json_append_string(&json,
                                      ch_policy_selection_mode(group->type)) &&
                ch_json_append(&json, ",\"selection_reason\":") &&
                ch_json_append_string(&json, group->selection_reason);
        }
        if (okay && group->updated_ts_ns != 0) {
            okay = ch_json_append_format(&json, ",\"updated_ts_ns\":%" PRId64,
                                         group->updated_ts_ns);
        }
        if (okay) okay = ch_json_append(&json, ",\"results\":[");
        size_t emitted = 0U;
        for (size_t chain_index = 0U; okay &&
             chain_index < group->chain_count; ++chain_index) {
            ch_policy_chain_state *chain = &group->chains[chain_index];
            if (!chain->has_result) continue;
            okay = (emitted == 0U || ch_json_append(&json, ",")) &&
                ch_policy_append_result(&json, chain);
            ++emitted;
        }
        if (okay) okay = ch_json_append(&json, "]}");
    }
    if (okay) okay = ch_json_append(&json, "]}");
    (void)pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode policy snapshot");
    }
    return result;
}

void ch_policy_manager_destroy(ch_policy_manager *manager) {
    if (manager == NULL) return;
    ch_policy_manager_stop(manager);
    for (size_t index = 0U; index < manager->group_count; ++index) {
        ch_policy_group_clear(&manager->groups[index]);
    }
    free(manager->groups);
    (void)pthread_cond_destroy(&manager->condition);
    (void)pthread_mutex_destroy(&manager->mutex);
    free(manager);
}
