// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/rules.h"

#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include "clambhook/config.h"
#include "clambhook/rule_feed.h"
#include "internal.h"

typedef struct config_strings {
    char **items;
    size_t count;
} config_strings;

typedef struct config_ports {
    int *items;
    size_t count;
} config_ports;

typedef struct config_rule_owner {
    char *name;
    char *action;
    config_strings rule_sets;
    config_strings domains;
    config_strings domain_suffixes;
    config_strings domain_keywords;
    config_strings cidrs;
    config_strings source_cidrs;
    config_ports ports;
    config_strings networks;
    config_strings processes;
} config_rule_owner;

typedef struct config_rule_set_owner {
    char *name;
    config_strings domains;
    config_strings domain_suffixes;
    config_strings domain_keywords;
    config_strings cidrs;
} config_rule_set_owner;

static void config_strings_clear(config_strings *strings) {
    if (strings == NULL) return;
    for (size_t index = 0U; index < strings->count; ++index) {
        free(strings->items[index]);
    }
    free(strings->items);
    memset(strings, 0, sizeof(*strings));
}

static ch_string_list config_strings_view(const config_strings *strings) {
    return (ch_string_list){
        .items = (const char *const *)strings->items,
        .count = strings->count
    };
}

static ch_status config_load_strings(const ch_config_table *table,
                                     const char *key,
                                     config_strings *out,
                                     ch_error *error) {
    const ch_config_array *array = ch_config_table_get_array(table, key);
    memset(out, 0, sizeof(*out));
    if (array == NULL) return CH_OK;
    size_t count = ch_config_array_count(array);
    if (count == 0U) return CH_OK;
    if (count > SIZE_MAX / sizeof(*out->items)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "configuration array is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    out->items = calloc(count, sizeof(*out->items));
    if (out->items == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate configuration strings");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        if (ch_config_array_get_string(array, index, &out->items[index], error) != CH_OK) {
            config_strings_clear(out);
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
        ++out->count;
    }
    return CH_OK;
}

static ch_status config_strings_take(config_strings *target, char ***items,
                                     size_t *count, ch_error *error) {
    if (items == NULL || count == NULL || *count == 0U) return CH_OK;
    if (*count > SIZE_MAX - target->count ||
        target->count + *count > SIZE_MAX / sizeof(*target->items)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "configuration string list is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t next_count = target->count + *count;
    char **grown = realloc(target->items,
                           next_count * sizeof(*target->items));
    if (grown == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "grow configuration string list");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(grown + target->count, *items, *count * sizeof(*grown));
    free(*items);
    target->items = grown;
    target->count = next_count;
    *items = NULL;
    *count = 0U;
    return CH_OK;
}

static int config_optional_bool(const ch_config_table *table,
                                const char *key, int fallback) {
    bool value = false;
    ch_error ignored;
    return table != NULL && ch_config_table_has(table, key) &&
        ch_config_table_get_bool(table, key, &value, &ignored) == CH_OK ?
        (value ? 1 : 0) : fallback;
}

static char *config_string_or(const ch_config_table *table,
                              const char *key, const char *fallback) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || !ch_config_table_has(table, key) ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup(fallback);
    }
    return value;
}

static ch_status config_load_ports(const ch_config_table *table,
                                   config_ports *out,
                                   ch_error *error) {
    const ch_config_array *array = ch_config_table_get_array(table, "ports");
    memset(out, 0, sizeof(*out));
    if (array == NULL) return CH_OK;
    size_t count = ch_config_array_count(array);
    if (count == 0U) return CH_OK;
    if (count > SIZE_MAX / sizeof(*out->items)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "configuration port array is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    out->items = calloc(count, sizeof(*out->items));
    if (out->items == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate configuration ports");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        int64_t port = 0;
        if (ch_config_array_get_int(array, index, &port, error) != CH_OK ||
            port < 0 || port > 65535) {
            free(out->items);
            memset(out, 0, sizeof(*out));
            if (error == NULL || error->code == CH_OK) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "configuration rule port is out of range");
            }
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        out->items[out->count++] = (int)port;
    }
    return CH_OK;
}

static void config_rule_owner_clear(config_rule_owner *owner) {
    if (owner == NULL) return;
    free(owner->name);
    free(owner->action);
    config_strings_clear(&owner->rule_sets);
    config_strings_clear(&owner->domains);
    config_strings_clear(&owner->domain_suffixes);
    config_strings_clear(&owner->domain_keywords);
    config_strings_clear(&owner->cidrs);
    config_strings_clear(&owner->source_cidrs);
    free(owner->ports.items);
    config_strings_clear(&owner->networks);
    config_strings_clear(&owner->processes);
    memset(owner, 0, sizeof(*owner));
}

static ch_status config_load_rule(const ch_config_table *table,
                                  config_rule_owner *owner,
                                  ch_rule_spec *spec,
                                  ch_error *error) {
    memset(owner, 0, sizeof(*owner));
    memset(spec, 0, sizeof(*spec));
    ch_status status = ch_config_table_get_string(table, "name", &owner->name, error);
    if (status == CH_OK) {
        status = ch_config_table_get_string(table, "action", &owner->action, error);
    }
    if (status == CH_OK) status = config_load_strings(table, "rule_sets", &owner->rule_sets, error);
    if (status == CH_OK) status = config_load_strings(table, "domains", &owner->domains, error);
    if (status == CH_OK) status = config_load_strings(table, "domain_suffixes", &owner->domain_suffixes, error);
    if (status == CH_OK) status = config_load_strings(table, "domain_keywords", &owner->domain_keywords, error);
    if (status == CH_OK) status = config_load_strings(table, "cidrs", &owner->cidrs, error);
    if (status == CH_OK) status = config_load_strings(table, "source_cidrs", &owner->source_cidrs, error);
    if (status == CH_OK) status = config_load_ports(table, &owner->ports, error);
    if (status == CH_OK) status = config_load_strings(table, "networks", &owner->networks, error);
    if (status == CH_OK) status = config_load_strings(table, "processes", &owner->processes, error);
    if (status != CH_OK) {
        config_rule_owner_clear(owner);
        return status;
    }
    *spec = (ch_rule_spec){
        .name = owner->name,
        .action = owner->action,
        .rule_sets = config_strings_view(&owner->rule_sets),
        .domains = config_strings_view(&owner->domains),
        .domain_suffixes = config_strings_view(&owner->domain_suffixes),
        .domain_keywords = config_strings_view(&owner->domain_keywords),
        .cidrs = config_strings_view(&owner->cidrs),
        .source_cidrs = config_strings_view(&owner->source_cidrs),
        .ports = {.items = owner->ports.items, .count = owner->ports.count},
        .networks = config_strings_view(&owner->networks),
        .processes = config_strings_view(&owner->processes)
    };
    return CH_OK;
}

static void config_rule_set_owner_clear(config_rule_set_owner *owner) {
    if (owner == NULL) return;
    free(owner->name);
    config_strings_clear(&owner->domains);
    config_strings_clear(&owner->domain_suffixes);
    config_strings_clear(&owner->domain_keywords);
    config_strings_clear(&owner->cidrs);
    memset(owner, 0, sizeof(*owner));
}

static ch_status config_load_rule_set(const ch_config_table *table,
                                      config_rule_set_owner *owner,
                                      ch_rule_set_spec *spec,
                                      ch_error *error) {
    memset(owner, 0, sizeof(*owner));
    memset(spec, 0, sizeof(*spec));
    ch_status status = ch_config_table_get_string(table, "name", &owner->name, error);
    if (status == CH_OK) status = config_load_strings(table, "domains", &owner->domains, error);
    if (status == CH_OK) status = config_load_strings(table, "domain_suffixes", &owner->domain_suffixes, error);
    if (status == CH_OK) status = config_load_strings(table, "domain_keywords", &owner->domain_keywords, error);
    if (status == CH_OK) status = config_load_strings(table, "cidrs", &owner->cidrs, error);
    if (status != CH_OK) {
        config_rule_set_owner_clear(owner);
        return status;
    }
    *spec = (ch_rule_set_spec){
        .name = owner->name,
        .domains = config_strings_view(&owner->domains),
        .domain_suffixes = config_strings_view(&owner->domain_suffixes),
        .domain_keywords = config_strings_view(&owner->domain_keywords),
        .cidrs = config_strings_view(&owner->cidrs)
    };
    return CH_OK;
}

static ch_status config_extend_rule_set_from_cache(
    const ch_config *config, const char *profile_name,
    const ch_config_table *table, config_rule_set_owner *owner,
    ch_rule_set_spec *spec, ch_error *error) {
    const char *config_path = ch_config_source_path(config);
    if (config_path == NULL || config_path[0] == '\0' ||
        config_optional_bool(table, "disabled", 0)) return CH_OK;
    char *url = config_string_or(table, "url", "");
    if (url == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule set URL");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (url[0] == '\0') {
        free(url);
        return CH_OK;
    }
    ch_rule_feed_cache cache;
    ch_error cache_error;
    ch_status status = ch_rule_feed_cache_load(
        config_path, CH_RULE_FEED_RULE_SET, profile_name, owner->name, url,
        &cache, &cache_error);
    free(url);
    if (status == CH_ERROR_NOT_FOUND || status == CH_ERROR_PARSE ||
        status == CH_ERROR_IO) {
        return CH_OK;
    }
    if (status != CH_OK) {
        *error = cache_error;
        return status;
    }
    status = config_strings_take(&owner->domain_suffixes,
                                 &cache.feed.domain_suffixes,
                                 &cache.feed.domain_suffix_count, error);
    if (status == CH_OK) {
        status = config_strings_take(&owner->cidrs, &cache.feed.cidrs,
                                     &cache.feed.cidr_count, error);
    }
    ch_rule_feed_cache_clear(&cache);
    if (status == CH_OK) {
        spec->domain_suffixes = config_strings_view(&owner->domain_suffixes);
        spec->cidrs = config_strings_view(&owner->cidrs);
    }
    return status;
}

static ch_status config_load_subscription_rule(
    const ch_config_table *table, const char *subscription_name,
    const char *kind, char ***cached_items, size_t *cached_count,
    config_rule_owner *owner, ch_rule_spec *spec, ch_error *error) {
    memset(owner, 0, sizeof(*owner));
    memset(spec, 0, sizeof(*spec));
    int length = snprintf(NULL, 0, "subscription:%s:%s", subscription_name,
                          kind);
    owner->name = length < 0 ? NULL : malloc((size_t)length + 1U);
    owner->action = config_string_or(table, "action", "block");
    if (owner->name != NULL) {
        (void)snprintf(owner->name, (size_t)length + 1U,
                       "subscription:%s:%s", subscription_name, kind);
    }
    ch_status status = owner->name == NULL || owner->action == NULL ?
        CH_ERROR_OUT_OF_MEMORY : config_load_strings(
            table, "networks", &owner->networks, error);
    if (status == CH_ERROR_OUT_OF_MEMORY) {
        ch_error_set(error, status, "allocate generated subscription rule");
    }
    if (status == CH_OK && strcmp(kind, "domains") == 0) {
        status = config_strings_take(&owner->domain_suffixes, cached_items,
                                     cached_count, error);
    } else if (status == CH_OK) {
        status = config_strings_take(&owner->cidrs, cached_items,
                                     cached_count, error);
    }
    if (status != CH_OK) {
        config_rule_owner_clear(owner);
        return status;
    }
    *spec = (ch_rule_spec){
        .name = owner->name,
        .action = owner->action,
        .domain_suffixes = config_strings_view(&owner->domain_suffixes),
        .cidrs = config_strings_view(&owner->cidrs),
        .networks = config_strings_view(&owner->networks)
    };
    return CH_OK;
}

static const ch_config_table *config_select_profile(const ch_config *config,
                                                     const char *profile_name,
                                                     ch_error *error) {
    const ch_config_table *profile;
    if (config == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "configuration is required");
        return NULL;
    }
    if (profile_name != NULL && profile_name[0] != '\0') {
        profile = ch_config_profile_named(config, profile_name);
        if (profile == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found", profile_name);
        }
        return profile;
    }
    profile = ch_config_active_profile(config);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "active profile not found");
    }
    return profile;
}

static ch_status config_load_names(const ch_config_array *array,
                                   config_strings *names,
                                   ch_error *error) {
    memset(names, 0, sizeof(*names));
    size_t count = ch_config_array_count(array);
    if (count == 0U) return CH_OK;
    if (count > SIZE_MAX / sizeof(*names->items)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "configuration collection is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    names->items = calloc(count, sizeof(*names->items));
    if (names->items == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate configuration names");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        if (table == NULL || ch_config_table_get_string(table, "name",
                                                        &names->items[index], error) != CH_OK) {
            config_strings_clear(names);
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
        ++names->count;
    }
    return CH_OK;
}

ch_rule_engine *ch_rule_engine_compile_config(const ch_config *config,
                                               const char *profile_name,
                                               ch_error *error) {
    ch_error_clear(error);
    const ch_config_table *profile = config_select_profile(config, profile_name, error);
    if (profile == NULL) return NULL;
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_array *groups = ch_config_table_get_array(profile, "policy_group");
    const ch_config_array *sets = ch_config_table_get_array(profile, "rule_set");
    const ch_config_array *rules = ch_config_table_get_array(profile, "rule");
    const ch_config_array *subscriptions = ch_config_table_get_array(
        profile, "rule_subscription");
    size_t set_count = ch_config_array_count(sets);
    size_t configured_rule_count = ch_config_array_count(rules);
    size_t subscription_count = ch_config_array_count(subscriptions);
    size_t rule_capacity = configured_rule_count;
    if (subscription_count <= (SIZE_MAX - rule_capacity) / 2U) {
        rule_capacity += subscription_count * 2U;
    } else {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "generated subscription rule count is too large");
        return NULL;
    }
    size_t rule_count = 0U;
    char *selected_profile_name = NULL;
    config_strings chain_names = {0};
    config_strings group_names = {0};
    config_rule_set_owner *set_owners = NULL;
    ch_rule_set_spec *set_specs = NULL;
    config_rule_owner *rule_owners = NULL;
    ch_rule_spec *rule_specs = NULL;
    ch_rule_engine *engine = NULL;
    ch_status status = config_load_names(chains, &chain_names, error);
    if (status == CH_OK) {
        status = ch_config_table_get_string(profile, "name",
                                            &selected_profile_name, error);
    }
    if (status == CH_OK && chain_names.count == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "profile has no chains");
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    if (status == CH_OK) status = config_load_names(groups, &group_names, error);
    if (status == CH_OK && set_count > 0U) {
        set_owners = calloc(set_count, sizeof(*set_owners));
        set_specs = calloc(set_count, sizeof(*set_specs));
        if (set_owners == NULL || set_specs == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate rule set configuration");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
    }
    for (size_t index = 0U; status == CH_OK && index < set_count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(sets, index);
        if (table == NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "rule set configuration is not a table");
            status = CH_ERROR_PARSE;
        } else {
            status = config_load_rule_set(table, &set_owners[index], &set_specs[index], error);
            if (status == CH_OK) {
                status = config_extend_rule_set_from_cache(
                    config, selected_profile_name, table, &set_owners[index],
                    &set_specs[index], error);
            }
        }
    }
    if (status == CH_OK && rule_capacity > 0U) {
        rule_owners = calloc(rule_capacity, sizeof(*rule_owners));
        rule_specs = calloc(rule_capacity, sizeof(*rule_specs));
        if (rule_owners == NULL || rule_specs == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate rule configuration");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
    }
    for (size_t index = 0U; status == CH_OK &&
         index < configured_rule_count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(rules, index);
        if (table == NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "rule configuration is not a table");
            status = CH_ERROR_PARSE;
        } else {
            status = config_load_rule(table, &rule_owners[rule_count],
                                      &rule_specs[rule_count], error);
            if (status == CH_OK) ++rule_count;
        }
    }
    for (size_t index = 0U; status == CH_OK && index < subscription_count;
         ++index) {
        const ch_config_table *table = ch_config_array_get_table(
            subscriptions, index);
        if (table == NULL || config_optional_bool(table, "disabled", 0)) {
            continue;
        }
        char *name = config_string_or(table, "name", "");
        char *url = config_string_or(table, "url", "");
        if (name == NULL || url == NULL) {
            free(name); free(url);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy subscription cache identity");
            status = CH_ERROR_OUT_OF_MEMORY;
            break;
        }
        ch_rule_feed_cache cache;
        ch_error cache_error;
        ch_status cache_status = ch_rule_feed_cache_load(
            ch_config_source_path(config), CH_RULE_FEED_SUBSCRIPTION,
            selected_profile_name, name, url, &cache, &cache_error);
        free(url);
        if (cache_status == CH_OK && cache.feed.domain_suffix_count > 0U) {
            status = config_load_subscription_rule(
                table, name, "domains", &cache.feed.domain_suffixes,
                &cache.feed.domain_suffix_count, &rule_owners[rule_count],
                &rule_specs[rule_count], error);
            if (status == CH_OK) ++rule_count;
        }
        if (cache_status == CH_OK && status == CH_OK &&
            cache.feed.cidr_count > 0U) {
            status = config_load_subscription_rule(
                table, name, "cidrs", &cache.feed.cidrs,
                &cache.feed.cidr_count, &rule_owners[rule_count],
                &rule_specs[rule_count], error);
            if (status == CH_OK) ++rule_count;
        }
        if (cache_status == CH_OK) ch_rule_feed_cache_clear(&cache);
        free(name);
    }
    if (status == CH_OK) {
        engine = ch_rule_engine_compile(
            rule_specs, rule_count, chain_names.items[0],
            config_strings_view(&chain_names), config_strings_view(&group_names),
            set_specs, set_count, error
        );
    }
    for (size_t index = 0U; rule_owners != NULL && index < rule_count; ++index) {
        config_rule_owner_clear(&rule_owners[index]);
    }
    for (size_t index = 0U; set_owners != NULL && index < set_count; ++index) {
        config_rule_set_owner_clear(&set_owners[index]);
    }
    free(rule_owners);
    free(rule_specs);
    free(set_owners);
    free(set_specs);
    config_strings_clear(&group_names);
    config_strings_clear(&chain_names);
    free(selected_profile_name);
    return engine;
}

static const ch_config_table *config_find_named_table(const ch_config_array *array,
                                                       const char *name) {
    size_t count = ch_config_array_count(array);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        char *candidate = NULL;
        ch_error ignored;
        int matches = table != NULL &&
            ch_config_table_get_string(table, "name", &candidate, &ignored) == CH_OK &&
            strcmp(candidate, name == NULL ? "" : name) == 0;
        free(candidate);
        if (matches) return table;
    }
    return NULL;
}

static char *config_optional_string(const ch_config_table *table,
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

static char *config_select_group_chain(const ch_config_table *profile,
                                       const char *group_name,
                                       ch_error *error) {
    const ch_config_array *groups = ch_config_table_get_array(profile, "policy_group");
    const ch_config_table *group = config_find_named_table(groups, group_name);
    if (group == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "policy group %s not found", group_name);
        return NULL;
    }
    char *type = config_optional_string(group, "type");
    char *selected = NULL;
    if (type == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy policy group type");
        return NULL;
    }
    if (strcasecmp(type, "select") == 0) {
        selected = config_optional_string(group, "selected");
        if (selected != NULL && selected[0] == '\0') {
            free(selected);
            selected = NULL;
        }
    }
    free(type);
    if (selected == NULL) {
        const ch_config_array *chains = ch_config_table_get_array(group, "chains");
        if (ch_config_array_count(chains) > 0U &&
            ch_config_array_get_string(chains, 0U, &selected, error) != CH_OK) {
            return NULL;
        }
    }
    if (selected == NULL || selected[0] == '\0') {
        free(selected);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy group %s has no member chains", group_name);
        return NULL;
    }
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    if (config_find_named_table(chains, selected) == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "policy group %s selected missing chain %s", group_name, selected);
        free(selected);
        return NULL;
    }
    return selected;
}

static int config_append_decision(ch_json_buffer *json,
                                  const ch_rule_decision *decision,
                                  const char *final_chain) {
    return ch_json_append(json, "{\"rule_name\":") &&
        ch_json_append_string(json, decision->rule_name) &&
        ch_json_append_format(json, ",\"rule_number\":%zu,\"action\":", decision->rule_number) &&
        ch_json_append_string(json, decision->action) &&
        ch_json_append(json, ",\"chain_name\":") &&
        ch_json_append_string(json, final_chain) &&
        ch_json_append(json, ",\"group_name\":") &&
        ch_json_append_string(json, decision->group_name) &&
        ch_json_append(json, ",\"target\":") &&
        ch_json_append_string(json, decision->target) &&
        ch_json_append(json, ",\"target_host\":") &&
        ch_json_append_string(json, decision->host) &&
        ch_json_append(json, ",\"target_port\":") &&
        ch_json_append_string(json, decision->port) &&
        ch_json_append(json, ",\"network\":") &&
        ch_json_append_string(json, decision->network) &&
        ch_json_append(json, ",\"source\":") &&
        ch_json_append_string(json, decision->source) &&
        ch_json_append_format(json, ",\"default\":%s,\"elapsed_ns\":%lld,\"explanation\":{\"source\":\"config\",\"rule_name\":",
                              decision->is_default ? "true" : "false", decision->elapsed_ns) &&
        ch_json_append_string(json, decision->rule_name) &&
        ch_json_append_format(json, ",\"rule_number\":%zu,\"matcher_kind\":", decision->rule_number) &&
        ch_json_append_string(json, decision->matcher_kind) &&
        ch_json_append(json, ",\"matcher_value\":") &&
        ch_json_append_string(json, decision->matcher_value) &&
        ch_json_append(json, ",\"policy_group\":") &&
        ch_json_append_string(json, decision->group_name) &&
        ch_json_append(json, ",\"selected_chain\":") &&
        ch_json_append_string(json, final_chain) &&
        ch_json_append(json, ",\"final_chain\":") &&
        ch_json_append_string(json, final_chain) &&
        ch_json_append(json, ",\"summary\":") &&
        ch_json_append_string(json, decision->summary) &&
        ch_json_append(json, "}}");
}

static int config_append_chain(ch_json_buffer *json,
                               const ch_config_table *chain) {
    char *name = config_optional_string(chain, "name");
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    size_t count = ch_config_array_count(servers);
    int okay = name != NULL && ch_json_append(json, ",\"chain\":{\"name\":") &&
        ch_json_append_string(json, name) &&
        ch_json_append_format(json, ",\"hop_count\":%zu},\"hops\":[", count);
    free(name);
    for (size_t index = 0U; okay && index < count; ++index) {
        const ch_config_table *server = ch_config_array_get_table(servers, index);
        char *server_name = config_optional_string(server, "name");
        char *address = config_optional_string(server, "address");
        char *protocol = config_optional_string(server, "protocol");
        okay = server_name != NULL && address != NULL && protocol != NULL &&
            (index == 0U || ch_json_append(json, ",")) &&
            ch_json_append(json, "{\"name\":") &&
            ch_json_append_string(json, server_name) &&
            ch_json_append(json, ",\"address\":") &&
            ch_json_append_string(json, address) &&
            ch_json_append(json, ",\"protocol\":") &&
            ch_json_append_string(json, protocol) &&
            ch_json_append(json, "}");
        free(server_name);
        free(address);
        free(protocol);
    }
    return okay && ch_json_append(json, "]");
}

ch_status ch_rule_explain_config_json(const ch_config *config,
                                      const char *profile_name,
                                      const ch_rule_match_context *context,
                                      char **out_json,
                                      ch_error *error) {
    ch_error_clear(error);
    if (context == NULL || out_json == NULL || context->target == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "match context, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    if (context->network == NULL ||
        (strcasecmp(context->network, "tcp") != 0 && strcasecmp(context->network, "udp") != 0)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "network must be tcp or udp");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const ch_config_table *profile = config_select_profile(config, profile_name, error);
    if (profile == NULL) return error == NULL ? CH_ERROR_NOT_FOUND : error->code;
    char *selected_profile = config_optional_string(profile, "name");
    if (selected_profile == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy selected profile name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_rule_engine *engine = ch_rule_engine_compile_config(config, selected_profile, error);
    if (engine == NULL) {
        free(selected_profile);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    ch_rule_decision decision;
    ch_status status = ch_rule_engine_decide(engine, context, &decision, error);
    ch_rule_engine_destroy(engine);
    if (status != CH_OK) {
        free(selected_profile);
        return status;
    }
    errno = 0;
    char *port_end = NULL;
    long port = strtol(decision.port, &port_end, 10);
    if (decision.host[0] == '\0' || decision.port[0] == '\0' || errno != 0 ||
        port_end == decision.port || *port_end != '\0' || port < 1L || port > 65535L) {
        ch_rule_decision_clear(&decision);
        free(selected_profile);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "target must be host:port with a port between 1 and 65535");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *group_chain = NULL;
    const char *final_chain = decision.chain_name;
    if (strcmp(decision.action, CH_RULE_ACTION_GROUP) == 0) {
        group_chain = config_select_group_chain(profile, decision.group_name, error);
        if (group_chain == NULL) {
            ch_rule_decision_clear(&decision);
            free(selected_profile);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        final_chain = group_chain;
    }
    const ch_config_table *chain = final_chain[0] == '\0' ? NULL :
        config_find_named_table(ch_config_table_get_array(profile, "chain"), final_chain);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, selected_profile) &&
        ch_json_append(&json, ",\"decision\":") &&
        config_append_decision(&json, &decision, final_chain);
    if (okay && chain != NULL) okay = config_append_chain(&json, chain);
    if (okay && chain == NULL) okay = ch_json_append(&json, ",\"hops\":[]");
    if (okay) okay = ch_json_append(&json, "}");
    if (!okay) {
        ch_json_dispose(&json);
        status = CH_ERROR_OUT_OF_MEMORY;
        ch_error_set(error, status, "encode route explanation");
    } else {
        *out_json = ch_json_take(&json);
        if (*out_json == NULL) {
            status = CH_ERROR_OUT_OF_MEMORY;
            ch_error_set(error, status, "encode route explanation");
        }
    }
    free(group_chain);
    ch_rule_decision_clear(&decision);
    free(selected_profile);
    return status;
}
