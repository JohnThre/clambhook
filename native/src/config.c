// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/config.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <math.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#include "internal.h"
#include "toml.h"

struct ch_config {
    toml_table_t *root;
    char *source_path;
    char *document;
};

static const toml_table_t *as_toml_table(const ch_config_table *table) {
    return (const toml_table_t *)(const void *)table;
}

static const toml_array_t *as_toml_array(const ch_config_array *array) {
    return (const toml_array_t *)(const void *)array;
}

static const ch_config_table *as_config_table(const toml_table_t *table) {
    return (const ch_config_table *)(const void *)table;
}

static const ch_config_array *as_config_array(const toml_array_t *array) {
    return (const ch_config_array *)(const void *)array;
}

static bool size_to_index(size_t index, int *out_index) {
    if (index > (size_t)INT_MAX) {
        return false;
    }
    *out_index = (int)index;
    return true;
}

static bool string_is_trimmed_nonempty(const char *value) {
    size_t length;
    if (value == NULL || value[0] == '\0') {
        return false;
    }
    length = strlen(value);
    return !isspace((unsigned char)value[0]) &&
           !isspace((unsigned char)value[length - 1U]);
}

static bool string_is_trimmed(const char *value) {
    size_t length;
    if (value == NULL || value[0] == '\0') {
        return true;
    }
    length = strlen(value);
    return !isspace((unsigned char)value[0]) &&
           !isspace((unsigned char)value[length - 1U]);
}

static char *table_string(const toml_table_t *table, const char *key) {
    toml_datum_t value = toml_string_in(table, key);
    return value.ok != 0 ? value.u.s : NULL;
}

static bool table_bool_default(const toml_table_t *table, const char *key,
                               bool default_value) {
    toml_datum_t value = toml_bool_in(table, key);
    return value.ok != 0 ? value.u.b != 0 : default_value;
}

static bool table_int_default(const toml_table_t *table, const char *key,
                              int64_t default_value, int64_t *out_value) {
    toml_datum_t value = toml_int_in(table, key);
    *out_value = value.ok != 0 ? value.u.i : default_value;
    return value.ok != 0;
}

static ch_status validation_error(ch_error *error, const char *message) {
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "validate config: %s", message);
    return CH_ERROR_INVALID_ARGUMENT;
}

static ch_status validation_error_value(ch_error *error, const char *format,
                                        const char *value) {
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, format, value == NULL ? "" : value);
    return CH_ERROR_INVALID_ARGUMENT;
}

static bool valid_listen_address(const char *address, bool allow_zero_port) {
    const char *port_text;
    const char *separator;
    char *end = NULL;
    long port;
    size_t host_length;

    if (!string_is_trimmed_nonempty(address)) {
        return false;
    }
    if (address[0] == '[') {
        const char *closing = strchr(address, ']');
        if (closing == NULL || closing == address + 1 || closing[1] != ':') {
            return false;
        }
        separator = closing + 1;
    } else {
        separator = strrchr(address, ':');
        if (separator == NULL || separator == address ||
            memchr(address, ':', (size_t)(separator - address)) != NULL) {
            return false;
        }
    }
    host_length = (size_t)(separator - address);
    for (size_t index = 0U; index < host_length; ++index) {
        if (isspace((unsigned char)address[index])) {
            return false;
        }
    }
    port_text = separator + 1;
    if (*port_text == '\0') {
        return false;
    }
    errno = 0;
    port = strtol(port_text, &end, 10);
    if (errno != 0 || end == port_text || *end != '\0') {
        return false;
    }
    return port >= (allow_zero_port ? 0L : 1L) && port <= 65535L;
}

static bool valid_cidr(const char *text) {
    char address[INET6_ADDRSTRLEN];
    const char *slash;
    char *end = NULL;
    long prefix;
    size_t address_length;
    unsigned char bytes[sizeof(struct in6_addr)];
    int family;
    long max_prefix;

    if (!string_is_trimmed_nonempty(text)) {
        return false;
    }
    slash = strrchr(text, '/');
    if (slash == NULL || slash == text || slash[1] == '\0') {
        return false;
    }
    address_length = (size_t)(slash - text);
    if (address_length >= sizeof(address)) {
        return false;
    }
    memcpy(address, text, address_length);
    address[address_length] = '\0';
    family = strchr(address, ':') != NULL ? AF_INET6 : AF_INET;
    max_prefix = family == AF_INET6 ? 128L : 32L;
    if (inet_pton(family, address, bytes) != 1) {
        return false;
    }
    errno = 0;
    prefix = strtol(slash + 1, &end, 10);
    return errno == 0 && end != slash + 1 && *end == '\0' &&
           prefix >= 0L && prefix <= max_prefix;
}

static bool valid_ip(const char *text) {
    unsigned char bytes[sizeof(struct in6_addr)];
    if (!string_is_trimmed_nonempty(text)) {
        return false;
    }
    return inet_pton(AF_INET, text, bytes) == 1 || inet_pton(AF_INET6, text, bytes) == 1;
}

static bool valid_http_url(const char *text, bool https_only) {
    const char *host;
    const char *end;
    const char *prefix = https_only ? "https://" : NULL;
    if (!string_is_trimmed_nonempty(text)) {
        return false;
    }
    if (prefix != NULL) {
        if (strncmp(text, prefix, strlen(prefix)) != 0) {
            return false;
        }
        host = text + strlen(prefix);
    } else if (strncmp(text, "https://", 8U) == 0) {
        host = text + 8U;
    } else if (strncmp(text, "http://", 7U) == 0) {
        host = text + 7U;
    } else {
        return false;
    }
    end = strpbrk(host, "/?#");
    if (end == NULL) {
        end = host + strlen(host);
    }
    if (end == host) {
        return false;
    }
    for (const char *cursor = host; cursor < end; ++cursor) {
        if (isspace((unsigned char)*cursor)) {
            return false;
        }
    }
    return true;
}

static bool array_contains_string(const toml_array_t *array, const char *needle) {
    int count;
    if (array == NULL || needle == NULL) {
        return false;
    }
    count = toml_array_nelem(array);
    for (int index = 0; index < count; ++index) {
        toml_datum_t item = toml_string_at(array, index);
        bool matches = item.ok != 0 && strcmp(item.u.s, needle) == 0;
        if (item.ok != 0) {
            free(item.u.s);
        }
        if (matches) {
            return true;
        }
    }
    return false;
}

static ch_status validate_string_array(const toml_array_t *array, bool cidr,
                                       bool ip, ch_error *error,
                                       const char *field) {
    int count;
    if (array == NULL) {
        return CH_OK;
    }
    count = toml_array_nelem(array);
    for (int index = 0; index < count; ++index) {
        toml_datum_t item = toml_string_at(array, index);
        bool valid = item.ok != 0 && string_is_trimmed_nonempty(item.u.s);
        if (valid && cidr) {
            valid = valid_cidr(item.u.s);
        }
        if (valid && ip) {
            valid = valid_ip(item.u.s);
        }
        if (item.ok != 0) {
            free(item.u.s);
        }
        if (!valid) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "validate config: %s contains an invalid value", field);
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status validate_duration_field(const toml_table_t *table, const char *key,
                                         ch_error *error, const char *label,
                                         bool nonnegative) {
    toml_datum_t value = toml_string_in(table, key);
    int64_t nanoseconds;
    ch_status status;
    if (value.ok == 0) {
        return CH_OK;
    }
    status = ch_config_parse_duration_ns(value.u.s, &nanoseconds, error);
    free(value.u.s);
    if (status != CH_OK || (nonnegative && nanoseconds < 0)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "validate config: %s must be a valid%s duration", label,
                     nonnegative ? " non-negative" : "");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return CH_OK;
}

static ch_status validate_dns(const toml_table_t *profile, ch_error *error) {
    const toml_table_t *dns = toml_table_in(profile, "dns");
    const toml_array_t *upstreams;
    int count;
    if (dns == NULL) {
        return CH_OK;
    }
    if (validate_duration_field(dns, "timeout", error, "dns.timeout", true) != CH_OK) {
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (!table_bool_default(dns, "enabled", false)) {
        return CH_OK;
    }
    upstreams = toml_array_in(dns, "upstream");
    if (upstreams == NULL || (count = toml_array_nelem(upstreams)) == 0) {
        return validation_error(error, "dns requires at least one upstream when enabled");
    }
    for (int index = 0; index < count; ++index) {
        const toml_table_t *upstream = toml_table_at(upstreams, index);
        char *protocol;
        char *url;
        char *address;
        char *resolver;
        char *transport;
        if (upstream == NULL) {
            return validation_error(error, "dns upstream must be a table");
        }
        protocol = table_string(upstream, "protocol");
        url = table_string(upstream, "url");
        address = table_string(upstream, "address");
        resolver = table_string(upstream, "resolver");
        transport = table_string(upstream, "transport");
        if (protocol == NULL || !string_is_trimmed_nonempty(protocol)) {
            free(protocol); free(url); free(address); free(resolver); free(transport);
            return validation_error(error, "dns upstream protocol is required");
        }
        if (strcmp(protocol, "doh") == 0) {
            if (!valid_http_url(url, true) || (address != NULL && address[0] != '\0')) {
                free(protocol); free(url); free(address); free(resolver); free(transport);
                return validation_error(error, "doh upstream requires a valid https url");
            }
        } else if (strcmp(protocol, "dot") == 0 || strcmp(protocol, "doq") == 0) {
            if (!valid_listen_address(address, false) || (url != NULL && url[0] != '\0')) {
                free(protocol); free(url); free(address); free(resolver); free(transport);
                return validation_error(error, "dot/doq upstream requires a valid address");
            }
        } else if (strcmp(protocol, "controld") == 0) {
            if (!string_is_trimmed_nonempty(resolver) || strpbrk(resolver, " \t\r\n/") != NULL ||
                (transport != NULL && transport[0] != '\0' && strcmp(transport, "doh") != 0 &&
                 strcmp(transport, "dot") != 0 && strcmp(transport, "doq") != 0) ||
                (url != NULL && url[0] != '\0') || (address != NULL && address[0] != '\0')) {
                free(protocol); free(url); free(address); free(resolver); free(transport);
                return validation_error(error, "invalid controld upstream");
            }
        } else {
            free(protocol); free(url); free(address); free(resolver); free(transport);
            return validation_error(error, "dns protocol must be doh, dot, doq, or controld");
        }
        free(protocol); free(url); free(address); free(resolver); free(transport);
        if (validate_string_array(toml_array_in(upstream, "bootstrap_ips"), false,
                                  true, error, "dns.bootstrap_ips") != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status validate_listen(const toml_table_t *profile,
                                 const toml_array_t *chains, ch_error *error) {
    const toml_table_t *listen = toml_table_in(profile, "listen");
    const char *address_keys[] = {"socks5", "http"};
    const char *chain_keys[] = {"socks5_chain", "http_chain"};
    const char *max_keys[] = {"socks5_max_connections", "http_max_connections"};
    const char *timeout_keys[] = {"socks5_handshake_timeout", "http_handshake_timeout"};
    int chain_count = chains == NULL ? 0 : toml_array_nelem(chains);
    if (listen == NULL) {
        return CH_OK;
    }
    for (size_t kind = 0U; kind < 2U; ++kind) {
        char *address = table_string(listen, address_keys[kind]);
        char *chain = table_string(listen, chain_keys[kind]);
        int64_t max_connections = 0;
        if (address != NULL && address[0] != '\0') {
            if (!valid_listen_address(address, true)) {
                free(address); free(chain);
                return validation_error(error, "listener address must be host:port");
            }
            if (chain_count == 0 || (chain != NULL && chain[0] != '\0' &&
                                     !array_contains_string(NULL, chain))) {
                bool found = chain == NULL || chain[0] == '\0';
                for (int index = 0; !found && index < chain_count; ++index) {
                    const toml_table_t *item = toml_table_at(chains, index);
                    char *name = item == NULL ? NULL : table_string(item, "name");
                    found = name != NULL && strcmp(name, chain) == 0;
                    free(name);
                }
                if (chain_count == 0 || !found) {
                    free(address); free(chain);
                    return validation_error(error, "listener references an unknown chain");
                }
            }
        }
        (void)table_int_default(listen, max_keys[kind], 0, &max_connections);
        if (max_connections < 0) {
            free(address); free(chain);
            return validation_error(error, "listener maximum connections must be non-negative");
        }
        if (validate_duration_field(listen, timeout_keys[kind], error,
                                    timeout_keys[kind], true) != CH_OK) {
            free(address); free(chain);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        free(address); free(chain);
    }
    {
        const toml_table_t *tun = toml_table_in(listen, "tun");
        if (tun != NULL && table_bool_default(tun, "enabled", false)) {
            int64_t mtu = 0;
            char *chain = table_string(tun, "chain");
            bool found = chain == NULL || chain[0] == '\0';
            (void)table_int_default(tun, "mtu", 0, &mtu);
            if (mtu < 0) {
                free(chain);
                return validation_error(error, "listen.tun.mtu must be non-negative");
            }
            for (int index = 0; !found && index < chain_count; ++index) {
                const toml_table_t *item = toml_table_at(chains, index);
                char *name = item == NULL ? NULL : table_string(item, "name");
                found = name != NULL && strcmp(name, chain) == 0;
                free(name);
            }
            free(chain);
            if (chain_count == 0 || !found) {
                return validation_error(error, "listen.tun.chain references an unknown chain");
            }
            if (validate_string_array(toml_array_in(tun, "addresses"), true, false,
                                      error, "listen.tun.addresses") != CH_OK ||
                validate_string_array(toml_array_in(tun, "routes"), true, false,
                                      error, "listen.tun.routes") != CH_OK ||
                validate_string_array(toml_array_in(tun, "exclude_cidrs"), true, false,
                                      error, "listen.tun.exclude_cidrs") != CH_OK) {
                return CH_ERROR_INVALID_ARGUMENT;
            }
        }
    }
    return CH_OK;
}

static bool named_table_exists(const toml_array_t *tables, const char *name) {
    int count = tables == NULL ? 0 : toml_array_nelem(tables);
    for (int index = 0; index < count; ++index) {
        const toml_table_t *table = toml_table_at(tables, index);
        char *candidate = table == NULL ? NULL : table_string(table, "name");
        bool matches = candidate != NULL && name != NULL && strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return true;
    }
    return false;
}

static ch_status validate_named_table(const toml_array_t *tables, int index,
                                      const char *label, ch_error *error) {
    const toml_table_t *table = toml_table_at(tables, index);
    char *name = table == NULL ? NULL : table_string(table, "name");
    if (!string_is_trimmed_nonempty(name)) {
        free(name);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "validate config: %s name is required", label);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    for (int prior = 0; prior < index; ++prior) {
        const toml_table_t *other = toml_table_at(tables, prior);
        char *other_name = other == NULL ? NULL : table_string(other, "name");
        bool duplicate = other_name != NULL && strcmp(other_name, name) == 0;
        free(other_name);
        if (duplicate) {
            free(name);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "validate config: duplicate %s name", label);
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    free(name);
    return CH_OK;
}

static bool string_equals_one_of(const char *value, const char *const *allowed,
                                 size_t allowed_count, bool empty_allowed) {
    if (value == NULL || value[0] == '\0') return empty_allowed;
    for (size_t index = 0U; index < allowed_count; ++index) {
        if (strcmp(value, allowed[index]) == 0) return true;
    }
    return false;
}

static ch_status validate_policy_groups(const toml_table_t *profile,
                                        const toml_array_t *chains,
                                        ch_error *error) {
    const toml_array_t *groups = toml_array_in(profile, "policy_group");
    int count = groups == NULL ? 0 : toml_array_nelem(groups);
    static const char *const types[] = {
        "select", "url-test", "fallback", "load-balance", "smart"
    };
    for (int index = 0; index < count; ++index) {
        const toml_table_t *group = toml_table_at(groups, index);
        const toml_array_t *members;
        char *type;
        char *selected;
        char *test_url;
        if (group == NULL || validate_named_table(groups, index, "policy group", error) != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
        type = table_string(group, "type");
        if (!string_equals_one_of(type, types, sizeof(types) / sizeof(types[0]), false)) {
            free(type);
            return validation_error(error,
                "policy group type must be select, url-test, fallback, load-balance, or smart");
        }
        members = toml_array_in(group, "chains");
        if (members == NULL || toml_array_nelem(members) == 0) {
            free(type);
            return validation_error(error, "policy group requires at least one chain");
        }
        for (int member_index = 0; member_index < toml_array_nelem(members); ++member_index) {
            toml_datum_t member = toml_string_at(members, member_index);
            bool valid = member.ok != 0 && string_is_trimmed_nonempty(member.u.s) &&
                         named_table_exists(chains, member.u.s);
            if (valid) {
                for (int prior = 0; prior < member_index; ++prior) {
                    toml_datum_t other = toml_string_at(members, prior);
                    bool duplicate = other.ok != 0 && strcmp(other.u.s, member.u.s) == 0;
                    if (other.ok != 0) free(other.u.s);
                    if (duplicate) { valid = false; break; }
                }
            }
            if (member.ok != 0) free(member.u.s);
            if (!valid) {
                free(type);
                return validation_error(error, "policy group contains an invalid or duplicate chain");
            }
        }
        selected = table_string(group, "selected");
        if (selected != NULL && selected[0] != '\0' &&
            (!string_is_trimmed_nonempty(selected) || !array_contains_string(members, selected))) {
            free(type); free(selected);
            return validation_error(error, "policy group selected must be one of chains");
        }
        free(selected);
        test_url = table_string(group, "test_url");
        if (test_url != NULL && test_url[0] != '\0' && !valid_http_url(test_url, false)) {
            free(type); free(test_url);
            return validation_error(error, "policy group test_url must be a valid http or https URL");
        }
        free(test_url);
        if (validate_duration_field(group, "interval", error,
                                    "policy_group.interval", true) != CH_OK ||
            validate_duration_field(group, "timeout", error,
                                    "policy_group.timeout", true) != CH_OK) {
            free(type);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        free(type);
    }
    return CH_OK;
}

static ch_status validate_rule_sets(const toml_table_t *profile, ch_error *error) {
    const toml_array_t *sets = toml_array_in(profile, "rule_set");
    int count = sets == NULL ? 0 : toml_array_nelem(sets);
    static const char *const formats[] = {"auto", "plain", "hosts", "adblock"};
    for (int index = 0; index < count; ++index) {
        const toml_table_t *set = toml_table_at(sets, index);
        const char *string_fields[] = {"domains", "domain_suffixes", "domain_keywords"};
        char *url;
        char *format;
        size_t inline_count = 0U;
        if (set == NULL || validate_named_table(sets, index, "rule set", error) != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
        for (size_t field_index = 0U; field_index < 3U; ++field_index) {
            const toml_array_t *values = toml_array_in(set, string_fields[field_index]);
            if (validate_string_array(values, false, false, error,
                                      string_fields[field_index]) != CH_OK) {
                return CH_ERROR_INVALID_ARGUMENT;
            }
            inline_count += values == NULL ? 0U : (size_t)toml_array_nelem(values);
        }
        {
            const toml_array_t *cidrs = toml_array_in(set, "cidrs");
            if (validate_string_array(cidrs, true, false, error, "rule_set.cidrs") != CH_OK) {
                return CH_ERROR_INVALID_ARGUMENT;
            }
            inline_count += cidrs == NULL ? 0U : (size_t)toml_array_nelem(cidrs);
        }
        url = table_string(set, "url");
        if (url != NULL && url[0] != '\0' && !valid_http_url(url, false)) {
            free(url);
            return validation_error(error, "rule set url must be a valid http or https URL");
        }
        format = table_string(set, "format");
        if (!string_equals_one_of(format, formats, sizeof(formats) / sizeof(formats[0]), true)) {
            free(url); free(format);
            return validation_error(error, "rule set format must be auto, plain, hosts, or adblock");
        }
        if (inline_count == 0U && (url == NULL || url[0] == '\0')) {
            free(url); free(format);
            return validation_error(error, "rule set requires an inline matcher or url");
        }
        free(url); free(format);
    }
    return CH_OK;
}

static ch_status validate_network_array(const toml_array_t *networks,
                                        ch_error *error, const char *label) {
    int count = networks == NULL ? 0 : toml_array_nelem(networks);
    for (int index = 0; index < count; ++index) {
        toml_datum_t value = toml_string_at(networks, index);
        bool valid = value.ok != 0 && string_is_trimmed_nonempty(value.u.s) &&
                     (strcasecmp(value.u.s, "tcp") == 0 || strcasecmp(value.u.s, "udp") == 0);
        if (value.ok != 0) free(value.u.s);
        if (!valid) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "validate config: %s must contain only tcp or udp", label);
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status validate_rules(const toml_table_t *profile,
                                const toml_array_t *chains, ch_error *error) {
    const toml_array_t *groups = toml_array_in(profile, "policy_group");
    const toml_array_t *sets = toml_array_in(profile, "rule_set");
    const toml_array_t *rules = toml_array_in(profile, "rule");
    int count = rules == NULL ? 0 : toml_array_nelem(rules);
    const char *string_fields[] = {
        "domains", "domain_suffixes", "domain_keywords", "networks",
        "rule_sets", "processes"
    };
    for (int index = 0; index < count; ++index) {
        const toml_table_t *rule = toml_table_at(rules, index);
        char *name;
        char *action;
        const toml_array_t *rule_sets;
        if (rule == NULL) return validation_error(error, "rule must be a table");
        name = table_string(rule, "name");
        if (!string_is_trimmed_nonempty(name)) {
            free(name);
            return validation_error(error, "rule name is required");
        }
        free(name);
        action = table_string(rule, "action");
        if (!string_is_trimmed_nonempty(action)) {
            free(action);
            return validation_error(error, "rule action is required");
        }
        if (strcasecmp(action, "direct") != 0 && strcasecmp(action, "block") != 0 &&
            strcasecmp(action, "reject") != 0) {
            const char *target = strchr(action, ':');
            bool valid = target != NULL && target[1] != '\0';
            if (valid && strncasecmp(action, "chain:", 6U) == 0) {
                valid = named_table_exists(chains, target + 1);
            } else if (valid && strncasecmp(action, "group:", 6U) == 0) {
                valid = named_table_exists(groups, target + 1);
            } else {
                valid = false;
            }
            if (!valid) {
                free(action);
                return validation_error(error,
                    "rule action must be direct, block, reject, chain:<name>, or group:<name>");
            }
        }
        free(action);
        for (size_t field_index = 0U; field_index < 6U; ++field_index) {
            if (validate_string_array(toml_array_in(rule, string_fields[field_index]),
                                      false, false, error,
                                      string_fields[field_index]) != CH_OK) {
                return CH_ERROR_INVALID_ARGUMENT;
            }
        }
        rule_sets = toml_array_in(rule, "rule_sets");
        if (rule_sets != NULL && toml_array_nelem(rule_sets) > 0) {
            if (toml_array_in(rule, "domains") != NULL ||
                toml_array_in(rule, "domain_suffixes") != NULL ||
                toml_array_in(rule, "domain_keywords") != NULL ||
                toml_array_in(rule, "cidrs") != NULL) {
                return validation_error(error, "rule_sets cannot be combined with inline matchers");
            }
            for (int set_index = 0; set_index < toml_array_nelem(rule_sets); ++set_index) {
                toml_datum_t set_name = toml_string_at(rule_sets, set_index);
                bool valid = set_name.ok != 0 && named_table_exists(sets, set_name.u.s);
                if (set_name.ok != 0) free(set_name.u.s);
                if (!valid) return validation_error(error, "rule references an unknown rule set");
            }
        }
        if (validate_network_array(toml_array_in(rule, "networks"), error,
                                   "rule.networks") != CH_OK ||
            validate_string_array(toml_array_in(rule, "cidrs"), true, false,
                                  error, "rule.cidrs") != CH_OK ||
            validate_string_array(toml_array_in(rule, "source_cidrs"), true, false,
                                  error, "rule.source_cidrs") != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
        {
            const toml_array_t *ports = toml_array_in(rule, "ports");
            int port_count = ports == NULL ? 0 : toml_array_nelem(ports);
            for (int port_index = 0; port_index < port_count; ++port_index) {
                toml_datum_t port = toml_int_at(ports, port_index);
                if (port.ok == 0 || port.u.i < 0 || port.u.i > 65535) {
                    return validation_error(error, "rule port is out of range");
                }
            }
        }
    }
    return CH_OK;
}

static ch_status validate_rule_subscriptions(const toml_table_t *profile,
                                             ch_error *error) {
    const toml_array_t *subscriptions = toml_array_in(profile, "rule_subscription");
    int count = subscriptions == NULL ? 0 : toml_array_nelem(subscriptions);
    static const char *const formats[] = {"auto", "plain", "hosts", "adblock"};
    static const char *const actions[] = {"block", "reject"};
    for (int index = 0; index < count; ++index) {
        const toml_table_t *subscription = toml_table_at(subscriptions, index);
        char *url;
        char *format;
        char *action;
        if (subscription == NULL ||
            validate_named_table(subscriptions, index, "rule subscription", error) != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
        url = table_string(subscription, "url");
        format = table_string(subscription, "format");
        action = table_string(subscription, "action");
        if (!valid_http_url(url, false)) {
            free(url); free(format); free(action);
            return validation_error(error, "rule subscription url must be a valid http or https URL");
        }
        if (!string_equals_one_of(format, formats, sizeof(formats) / sizeof(formats[0]), true)) {
            free(url); free(format); free(action);
            return validation_error(error, "rule subscription has an invalid format");
        }
        if (!string_equals_one_of(action, actions, sizeof(actions) / sizeof(actions[0]), true)) {
            free(url); free(format); free(action);
            return validation_error(error, "rule subscription action must be block or reject");
        }
        free(url); free(format); free(action);
        if (validate_network_array(toml_array_in(subscription, "networks"), error,
                                   "rule_subscription.networks") != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status validate_profile(const toml_table_t *profile, ch_error *error) {
    const toml_array_t *chains = toml_array_in(profile, "chain");
    int chain_count = chains == NULL ? 0 : toml_array_nelem(chains);
    for (int index = 0; index < chain_count; ++index) {
        const toml_table_t *chain = toml_table_at(chains, index);
        const toml_array_t *servers;
        char *name;
        if (chain == NULL) {
            return validation_error(error, "chain must be a table");
        }
        name = table_string(chain, "name");
        if (!string_is_trimmed_nonempty(name)) {
            free(name);
            return validation_error(error, "chain name is required");
        }
        for (int prior = 0; prior < index; ++prior) {
            const toml_table_t *other = toml_table_at(chains, prior);
            char *other_name = other == NULL ? NULL : table_string(other, "name");
            bool duplicate = other_name != NULL && strcmp(other_name, name) == 0;
            free(other_name);
            if (duplicate) {
                free(name);
                return validation_error(error, "duplicate chain name");
            }
        }
        free(name);
        servers = toml_array_in(chain, "server");
        if (servers == NULL || toml_array_nelem(servers) == 0) {
            return validation_error(error, "chain requires at least one server");
        }
        for (int server_index = 0; server_index < toml_array_nelem(servers); ++server_index) {
            const toml_table_t *server = toml_table_at(servers, server_index);
            char *protocol = server == NULL ? NULL : table_string(server, "protocol");
            if (!string_is_trimmed_nonempty(protocol)) {
                free(protocol);
                return validation_error(error, "server protocol is required");
            }
            free(protocol);
        }
    }
    if (validate_policy_groups(profile, chains, error) != CH_OK ||
        validate_rule_sets(profile, error) != CH_OK ||
        validate_rules(profile, chains, error) != CH_OK ||
        validate_rule_subscriptions(profile, error) != CH_OK ||
        validate_listen(profile, chains, error) != CH_OK ||
        validate_dns(profile, error) != CH_OK) {
        return CH_ERROR_INVALID_ARGUMENT;
    }
    {
        const toml_table_t *api = toml_table_in(profile, "api");
        char *listen = api == NULL ? NULL : table_string(api, "listen");
        if (listen != NULL && listen[0] != '\0' && !valid_listen_address(listen, true)) {
            free(listen);
            return validation_error(error, "api.listen must be host:port");
        }
        free(listen);
    }
    {
        const toml_table_t *conditioner = toml_table_in(profile, "conditioner");
        if (conditioner != NULL &&
            (validate_duration_field(conditioner, "latency", error,
                                     "conditioner.latency", false) != CH_OK ||
             validate_duration_field(conditioner, "jitter", error,
                                     "conditioner.jitter", false) != CH_OK)) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static bool string_has_case_and_trim(const char *value, bool uppercase) {
    if (!string_is_trimmed_nonempty(value)) return false;
    for (const char *cursor = value; *cursor != '\0'; ++cursor) {
        unsigned char character = (unsigned char)*cursor;
        if (isalpha(character) &&
            (uppercase ? !isupper(character) : !islower(character))) return false;
    }
    return true;
}

static ch_status validate_case_array(const toml_array_t *array, bool uppercase,
                                     const char *label, ch_error *error) {
    int count = array == NULL ? 0 : toml_array_nelem(array);
    for (int index = 0; index < count; ++index) {
        toml_datum_t item = toml_string_at(array, index);
        bool valid = item.ok != 0 && string_has_case_and_trim(item.u.s, uppercase);
        if (item.ok != 0) free(item.u.s);
        if (!valid) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "validate config: %s contains an invalid value", label);
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status validate_developer_match(const toml_table_t *match,
                                          ch_error *error) {
    const char *fields[] = {"host", "path_prefix", "url_contains"};
    if (match == NULL) return CH_OK;
    for (size_t index = 0U; index < 3U; ++index) {
        char *value = table_string(match, fields[index]);
        bool valid = string_is_trimmed(value);
        free(value);
        if (!valid) return validation_error(error, "developer match values must not have surrounding whitespace");
    }
    return validate_case_array(toml_array_in(match, "methods"), true,
                               "developer.match.methods", error);
}

static ch_status validate_developer_rule_common(const toml_table_t *rule,
                                                ch_error *error) {
    char *id = table_string(rule, "id");
    char *name = table_string(rule, "name");
    bool valid_id = string_is_trimmed_nonempty(id);
    bool valid_name = string_is_trimmed(name);
    free(id); free(name);
    if (!valid_id) return validation_error(error, "developer rule id is required");
    if (!valid_name) return validation_error(error, "developer rule name must not have surrounding whitespace");
    return validate_developer_match(toml_table_in(rule, "match"), error);
}

static ch_status validate_developer_map_rules(const toml_table_t *developer,
                                              ch_error *error) {
    const toml_array_t *rules = toml_array_in(developer, "map_rule");
    int count = rules == NULL ? 0 : toml_array_nelem(rules);
    for (int index = 0; index < count; ++index) {
        const toml_table_t *rule = toml_table_at(rules, index);
        char *kind;
        char *local_path;
        char *remote_url;
        int64_t status = 0;
        if (rule == NULL || validate_developer_rule_common(rule, error) != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
        kind = table_string(rule, "kind");
        local_path = table_string(rule, "local_path");
        remote_url = table_string(rule, "remote_url");
        if (kind == NULL || (strcmp(kind, "local") != 0 && strcmp(kind, "remote") != 0)) {
            free(kind); free(local_path); free(remote_url);
            return validation_error(error, "developer map kind must be local or remote");
        }
        if (strcmp(kind, "local") == 0) {
            if (!string_is_trimmed_nonempty(local_path) || local_path[0] != '/' ||
                (remote_url != NULL && remote_url[0] != '\0')) {
                free(kind); free(local_path); free(remote_url);
                return validation_error(error, "developer local map requires an absolute local_path only");
            }
        } else if (!valid_http_url(remote_url, false) ||
                   (local_path != NULL && local_path[0] != '\0')) {
            free(kind); free(local_path); free(remote_url);
            return validation_error(error, "developer remote map requires a valid remote_url only");
        }
        free(kind); free(local_path); free(remote_url);
        (void)table_int_default(rule, "status", 0, &status);
        if (status < 0 || status > 999) {
            return validation_error(error, "developer map status must be between 0 and 999");
        }
        {
            const toml_table_t *headers = toml_table_in(rule, "headers");
            if (headers != NULL) {
                for (int key_index = 0;; ++key_index) {
                    const char *key = toml_key_in(headers, key_index);
                    if (key == NULL) break;
                    if (!string_is_trimmed_nonempty(key)) {
                        return validation_error(error, "developer map headers contains an invalid name");
                    }
                }
            }
        }
    }
    return CH_OK;
}

static ch_status validate_developer_stage(const toml_table_t *rule,
                                          char **out_stage, ch_error *error) {
    char *stage = table_string(rule, "stage");
    if (stage == NULL || (strcmp(stage, "request") != 0 &&
                          strcmp(stage, "response") != 0 && strcmp(stage, "both") != 0)) {
        free(stage);
        return validation_error(error, "developer rule stage must be request, response, or both");
    }
    *out_stage = stage;
    return CH_OK;
}

static ch_status validate_developer_breakpoint_rules(const toml_table_t *developer,
                                                     ch_error *error) {
    const toml_array_t *rules = toml_array_in(developer, "breakpoint_rule");
    int count = rules == NULL ? 0 : toml_array_nelem(rules);
    for (int index = 0; index < count; ++index) {
        const toml_table_t *rule = toml_table_at(rules, index);
        char *stage = NULL;
        if (rule == NULL || validate_developer_rule_common(rule, error) != CH_OK ||
            validate_developer_stage(rule, &stage, error) != CH_OK) {
            free(stage);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        free(stage);
    }
    return CH_OK;
}

static bool parse_http_status(const char *text) {
    char *end = NULL;
    long status;
    if (text == NULL || text[0] == '\0') return false;
    errno = 0;
    status = strtol(text, &end, 10);
    return errno == 0 && end != text && *end == '\0' && status >= 100L && status <= 599L;
}

static ch_status validate_developer_rewrite_op(const toml_table_t *op,
                                               const char *stage,
                                               ch_error *error) {
    char *target = table_string(op, "target");
    char *action = table_string(op, "action");
    char *field = table_string(op, "field");
    char *value = table_string(op, "value");
    char *replacement = table_string(op, "replace");
    bool valid = false;
    if (target != NULL && action != NULL && strcmp(target, "header") == 0) {
        valid = (strcmp(action, "add") == 0 || strcmp(action, "set") == 0 ||
                 strcmp(action, "remove") == 0) && string_is_trimmed_nonempty(field);
        if (valid && (strcmp(action, "add") == 0 || strcmp(action, "set") == 0)) {
            valid = value != NULL && value[0] != '\0';
        }
    } else if (target != NULL && action != NULL && strcmp(target, "body") == 0) {
        if (strcmp(action, "set") == 0) valid = value != NULL && value[0] != '\0';
        else if (strcmp(action, "replace") == 0) {
            valid = value != NULL && value[0] != '\0' &&
                    replacement != NULL && replacement[0] != '\0';
        }
    } else if (target != NULL && action != NULL && strcmp(target, "status") == 0) {
        valid = strcmp(action, "set") == 0 && strcmp(stage, "request") != 0 &&
                parse_http_status(value);
    }
    free(target); free(action); free(field); free(value); free(replacement);
    if (!valid) return validation_error(error, "developer rewrite contains an invalid op");
    return CH_OK;
}

static ch_status validate_developer_rewrite_rules(const toml_table_t *developer,
                                                  ch_error *error) {
    const toml_array_t *rules = toml_array_in(developer, "rewrite_rule");
    int count = rules == NULL ? 0 : toml_array_nelem(rules);
    for (int index = 0; index < count; ++index) {
        const toml_table_t *rule = toml_table_at(rules, index);
        const toml_array_t *operations;
        char *stage = NULL;
        if (rule == NULL || validate_developer_rule_common(rule, error) != CH_OK ||
            validate_developer_stage(rule, &stage, error) != CH_OK) {
            free(stage);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        operations = toml_array_in(rule, "op");
        if (operations == NULL || toml_array_nelem(operations) == 0) {
            free(stage);
            return validation_error(error, "developer rewrite rule requires at least one op");
        }
        for (int op_index = 0; op_index < toml_array_nelem(operations); ++op_index) {
            const toml_table_t *op = toml_table_at(operations, op_index);
            if (op == NULL || validate_developer_rewrite_op(op, stage, error) != CH_OK) {
                free(stage);
                return CH_ERROR_INVALID_ARGUMENT;
            }
        }
        free(stage);
    }
    return CH_OK;
}

static ch_status validate_developer(const toml_table_t *developer,
                                    ch_error *error) {
    const char *limit_keys[] = {
        "capture_limit", "body_limit_bytes", "header_value_limit_bytes"
    };
    int64_t value = 0;
    if (developer == NULL) return CH_OK;
    for (size_t index = 0U; index < 3U; ++index) {
        (void)table_int_default(developer, limit_keys[index], 0, &value);
        if (value < 0) return validation_error(error, "developer limits must be non-negative");
    }
    for (size_t index = 0U; index < 2U; ++index) {
        const char *key = index == 0U ? "ca_cert_path" : "ca_key_path";
        char *path = table_string(developer, key);
        bool valid = string_is_trimmed(path);
        free(path);
        if (!valid) return validation_error(error, "developer CA paths must not have surrounding whitespace");
    }
    if (validate_case_array(toml_array_in(developer, "redact_headers"), false,
                            "developer.redact_headers", error) != CH_OK ||
        validate_case_array(toml_array_in(developer, "redact_query_params"), false,
                            "developer.redact_query_params", error) != CH_OK ||
        validate_case_array(toml_array_in(developer, "ssl_decrypt_hosts"), false,
                            "developer.ssl_decrypt_hosts", error) != CH_OK ||
        validate_developer_map_rules(developer, error) != CH_OK ||
        validate_developer_breakpoint_rules(developer, error) != CH_OK ||
        validate_developer_rewrite_rules(developer, error) != CH_OK) {
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return CH_OK;
}

static ch_status validate_config(const ch_config *config, ch_error *error) {
    const toml_array_t *profiles = toml_array_in(config->root, "profile");
    int profile_count;
    char *active;
    if (profiles == NULL || (profile_count = toml_array_nelem(profiles)) == 0) {
        return validation_error(error, "at least one profile is required");
    }
    active = table_string(config->root, "active");
    if (!string_is_trimmed(active)) {
        free(active);
        return validation_error(error, "active profile must not have surrounding whitespace");
    }
    for (int index = 0; index < profile_count; ++index) {
        const toml_table_t *profile = toml_table_at(profiles, index);
        char *name = profile == NULL ? NULL : table_string(profile, "name");
        if (!string_is_trimmed_nonempty(name)) {
            free(name); free(active);
            return validation_error(error, "profile name is required");
        }
        for (int prior = 0; prior < index; ++prior) {
            const toml_table_t *other = toml_table_at(profiles, prior);
            char *other_name = other == NULL ? NULL : table_string(other, "name");
            bool duplicate = other_name != NULL && strcmp(other_name, name) == 0;
            free(other_name);
            if (duplicate) {
                free(name); free(active);
                return validation_error(error, "duplicate profile name");
            }
        }
        if (validate_profile(profile, error) != CH_OK) {
            free(name); free(active);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        free(name);
    }
    if (active != NULL && active[0] != '\0') {
        bool found = false;
        for (int index = 0; index < profile_count; ++index) {
            const toml_table_t *profile = toml_table_at(profiles, index);
            char *name = profile == NULL ? NULL : table_string(profile, "name");
            found = name != NULL && strcmp(name, active) == 0;
            free(name);
            if (found) break;
        }
        if (!found) {
            ch_status status = validation_error_value(error,
                "validate config: active profile \"%s\" not found", active);
            free(active);
            return status;
        }
    }
    free(active);
    {
        const toml_table_t *prompt = toml_table_in(config->root, "prompt");
        int64_t timeout = 0;
        char *silent;
        if (prompt != NULL) {
            (void)table_int_default(prompt, "timeout_seconds", 0, &timeout);
            if (timeout < 0) return validation_error(error, "prompt timeout_seconds must not be negative");
            silent = table_string(prompt, "silent_mode");
            if (silent != NULL && silent[0] != '\0' && strcmp(silent, "allow") != 0 &&
                strcmp(silent, "deny") != 0) {
                free(silent);
                return validation_error(error, "prompt silent_mode must be empty, allow, or deny");
            }
            free(silent);
        }
    }
    if (validate_developer(toml_table_in(config->root, "developer"), error) != CH_OK) {
        return CH_ERROR_INVALID_ARGUMENT;
    }
    {
        const toml_table_t *traffic = toml_table_in(config->root, "traffic");
        if (traffic != NULL &&
            validate_duration_field(traffic, "history_max_age", error,
                                    "traffic.history_max_age", false) != CH_OK) {
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

ch_status ch_config_load(const char *path, ch_config **out_config, ch_error *error) {
    FILE *file;
    long file_size;
    char *document;
    size_t read_count;
    ch_status status;
    ch_error_clear(error);
    if (path == NULL || path[0] == '\0' || out_config == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config path and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_config = NULL;
    file = fopen(path, "rb");
    if (file == NULL) {
        ch_error_set(error, CH_ERROR_IO, "read config: %s", strerror(errno));
        return CH_ERROR_IO;
    }
    if (fseek(file, 0L, SEEK_END) != 0 || (file_size = ftell(file)) < 0L ||
        fseek(file, 0L, SEEK_SET) != 0) {
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_IO, "read config size: %s", strerror(errno));
        return CH_ERROR_IO;
    }
    if ((uintmax_t)file_size > (uintmax_t)(SIZE_MAX - 1U)) {
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "config file is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    document = malloc((size_t)file_size + 1U);
    if (document == NULL) {
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate config document");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    read_count = fread(document, 1U, (size_t)file_size, file);
    if (read_count != (size_t)file_size || ferror(file) != 0) {
        free(document);
        (void)fclose(file);
        ch_error_set(error, CH_ERROR_IO, "read config: %s",
                     errno == 0 ? "short read" : strerror(errno));
        return CH_ERROR_IO;
    }
    document[read_count] = '\0';
    if (fclose(file) != 0) {
        free(document);
        ch_error_set(error, CH_ERROR_IO, "close config: %s", strerror(errno));
        return CH_ERROR_IO;
    }
    status = ch_config_parse(document, path, out_config, error);
    free(document);
    return status;
}

ch_status ch_config_parse(const char *toml, const char *source_path,
                          ch_config **out_config, ch_error *error) {
    char *input;
    char parse_error[256];
    toml_table_t *root;
    ch_config *config;
    ch_error_clear(error);
    if (toml == NULL || out_config == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "TOML and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_config = NULL;
    input = ch_strdup(toml);
    if (input == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy TOML input");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    root = toml_parse(input, parse_error, (int)sizeof(parse_error));
    free(input);
    if (root == NULL) {
        ch_error_set(error, CH_ERROR_PARSE, "parse config: %s", parse_error);
        return CH_ERROR_PARSE;
    }
    config = calloc(1U, sizeof(*config));
    if (config == NULL) {
        toml_free(root);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate config");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    config->root = root;
    config->document = ch_strdup(toml);
    if (config->document == NULL) {
        ch_config_free(config);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy config document");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (source_path != NULL) {
        config->source_path = ch_strdup(source_path);
        if (config->source_path == NULL) {
            ch_config_free(config);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy config path");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    if (validate_config(config, error) != CH_OK) {
        ch_config_free(config);
        return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
    }
    *out_config = config;
    return CH_OK;
}

void ch_config_free(ch_config *config) {
    if (config == NULL) return;
    toml_free(config->root);
    free(config->source_path);
    free(config->document);
    free(config);
}

const char *ch_config_source_path(const ch_config *config) {
    return config == NULL ? NULL : config->source_path;
}

const char *ch_config_document(const ch_config *config) {
    return config == NULL ? NULL : config->document;
}

const ch_config_table *ch_config_root(const ch_config *config) {
    return config == NULL ? NULL : as_config_table(config->root);
}

static int config_append_toml_string(ch_json_buffer *output,
                                     const char *value) {
    static const char hex[] = "0123456789abcdef";
    if (!ch_json_append(output, "\"")) return 0;
    const unsigned char *cursor = (const unsigned char *)value;
    for (; *cursor != 0U; ++cursor) {
        const char *escaped = NULL;
        switch (*cursor) {
            case '\b': escaped = "\\b"; break;
            case '\t': escaped = "\\t"; break;
            case '\n': escaped = "\\n"; break;
            case '\f': escaped = "\\f"; break;
            case '\r': escaped = "\\r"; break;
            case '"': escaped = "\\\""; break;
            case '\\': escaped = "\\\\"; break;
            default: break;
        }
        if (escaped != NULL) {
            if (!ch_json_append(output, escaped)) return 0;
        } else if (*cursor < 0x20U || *cursor == 0x7fU) {
            char encoded[7] = {
                '\\', 'u', '0', '0', hex[*cursor >> 4U],
                hex[*cursor & 0x0fU], '\0'
            };
            if (!ch_json_append(output, encoded)) return 0;
        } else {
            char character[2] = {(char)*cursor, '\0'};
            if (!ch_json_append(output, character)) return 0;
        }
    }
    return ch_json_append(output, "\"");
}

ch_status ch_config_document_set_active(const ch_config *config,
                                        const char *profile_name,
                                        char **out_toml,
                                        ch_error *error) {
    ch_error_clear(error);
    if (config == NULL || profile_name == NULL || profile_name[0] == '\0' ||
        out_toml == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config, profile name, and TOML output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_toml = NULL;
    if (ch_config_profile_named(config, profile_name) == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name);
        return CH_ERROR_NOT_FOUND;
    }
    const char *document = ch_config_document(config);
    if (document == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "config has no source document");
        return CH_ERROR_INVALID_STATE;
    }
    const char *active_start = NULL;
    const char *active_after = NULL;
    const char *cursor = document;
    while (*cursor != '\0') {
        const char *line_start = cursor;
        const char *line_end = strchr(cursor, '\n');
        if (line_end == NULL) line_end = cursor + strlen(cursor);
        const char *content = line_start;
        while (content < line_end &&
               (*content == ' ' || *content == '\t')) {
            ++content;
        }
        if (content < line_end && *content == '[') break;
        if ((size_t)(line_end - content) >= 6U &&
            strncmp(content, "active", 6U) == 0) {
            const char *separator = content + 6U;
            while (separator < line_end &&
                   (*separator == ' ' || *separator == '\t')) {
                ++separator;
            }
            if (separator < line_end && *separator == '=') {
                active_start = line_start;
                active_after = *line_end == '\n' ? line_end + 1U : line_end;
                break;
            }
        }
        cursor = *line_end == '\n' ? line_end + 1U : line_end;
    }

    ch_json_buffer rendered;
    ch_json_init(&rendered);
    int okay = 1;
    if (active_start != NULL) {
        okay = ch_json_append_bytes(
            &rendered, document, (size_t)(active_start - document));
    }
    if (okay) okay = ch_json_append(&rendered, "active = ");
    if (okay) okay = config_append_toml_string(&rendered, profile_name);
    if (okay) okay = ch_json_append(&rendered, "\n");
    if (okay) {
        okay = ch_json_append(
            &rendered, active_after == NULL ? document : active_after);
    }
    char *candidate = okay ? ch_json_take(&rendered) : NULL;
    ch_json_dispose(&rendered);
    if (candidate == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "render active profile document");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_config *validated = NULL;
    ch_status status = ch_config_parse(
        candidate, ch_config_source_path(config), &validated, error);
    ch_config_free(validated);
    if (status != CH_OK) {
        free(candidate);
        return status;
    }
    *out_toml = candidate;
    return CH_OK;
}

size_t ch_config_profile_count(const ch_config *config) {
    const toml_array_t *profiles;
    int count;
    if (config == NULL) return 0U;
    profiles = toml_array_in(config->root, "profile");
    count = profiles == NULL ? 0 : toml_array_nelem(profiles);
    return count < 0 ? 0U : (size_t)count;
}

const ch_config_table *ch_config_profile_at(const ch_config *config, size_t index) {
    const toml_array_t *profiles;
    int converted;
    if (config == NULL || !size_to_index(index, &converted)) return NULL;
    profiles = toml_array_in(config->root, "profile");
    return profiles == NULL ? NULL : as_config_table(toml_table_at(profiles, converted));
}

const ch_config_table *ch_config_active_profile(const ch_config *config) {
    const toml_array_t *profiles;
    char *active;
    int count;
    if (config == NULL) return NULL;
    profiles = toml_array_in(config->root, "profile");
    if (profiles == NULL) return NULL;
    count = toml_array_nelem(profiles);
    active = table_string(config->root, "active");
    if (active != NULL && active[0] != '\0') {
        for (int index = 0; index < count; ++index) {
            const toml_table_t *profile = toml_table_at(profiles, index);
            char *name = profile == NULL ? NULL : table_string(profile, "name");
            bool matches = name != NULL && strcmp(name, active) == 0;
            free(name);
            if (matches) {
                free(active);
                return as_config_table(profile);
            }
        }
    }
    free(active);
    return count > 0 ? as_config_table(toml_table_at(profiles, 0)) : NULL;
}

const ch_config_table *ch_config_profile_named(const ch_config *config,
                                                const char *name) {
    if (config == NULL || name == NULL || name[0] == '\0') return NULL;
    size_t count = ch_config_profile_count(config);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *profile = ch_config_profile_at(config, index);
        char *candidate = NULL;
        ch_error ignored;
        bool matches = ch_config_table_get_string(profile, "name", &candidate,
                                                   &ignored) == CH_OK &&
                       strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return profile;
    }
    return NULL;
}

static char *normalize_unix_path(const char *path) {
    char *copy = ch_strdup(path);
    char **segments;
    size_t segment_count = 0U;
    size_t length;
    bool absolute;
    char *cursor;
    char *result;
    size_t output_length;
    if (copy == NULL) return NULL;
    length = strlen(copy);
    absolute = copy[0] == '/';
    segments = calloc(length + 1U, sizeof(*segments));
    if (segments == NULL) { free(copy); return NULL; }
    cursor = copy;
    while (*cursor != '\0') {
        char *start;
        while (*cursor == '/') ++cursor;
        if (*cursor == '\0') break;
        start = cursor;
        while (*cursor != '\0' && *cursor != '/') ++cursor;
        if (*cursor != '\0') *cursor++ = '\0';
        if (strcmp(start, ".") == 0) continue;
        if (strcmp(start, "..") == 0) {
            if (segment_count > 0U && strcmp(segments[segment_count - 1U], "..") != 0) {
                --segment_count;
            } else if (!absolute) {
                segments[segment_count++] = start;
            }
            continue;
        }
        segments[segment_count++] = start;
    }
    output_length = absolute ? 1U : 0U;
    for (size_t index = 0U; index < segment_count; ++index) {
        output_length += strlen(segments[index]) + (index > 0U ? 1U : 0U);
    }
    if (output_length == 0U) output_length = 1U;
    result = malloc(output_length + 1U);
    if (result == NULL) { free(segments); free(copy); return NULL; }
    result[0] = '\0';
    if (absolute) (void)strcat(result, "/");
    for (size_t index = 0U; index < segment_count; ++index) {
        if (index > 0U) (void)strcat(result, "/");
        (void)strcat(result, segments[index]);
    }
    if (!absolute && segment_count == 0U) (void)strcpy(result, ".");
    free(segments);
    free(copy);
    return result;
}

ch_status ch_config_resolve_path(const ch_config *config, const char *configured_path,
                                 char **out_path, ch_error *error) {
    char working_directory[PATH_MAX];
    const char *source;
    const char *slash;
    char *joined;
    size_t base_length;
    size_t path_length;
    ch_error_clear(error);
    if (config == NULL || configured_path == NULL || out_path == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config, configured path, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_path = NULL;
    if (configured_path[0] == '\0') {
        *out_path = ch_strdup("");
    } else if (configured_path[0] == '/') {
        *out_path = normalize_unix_path(configured_path);
    } else {
        source = config->source_path;
        slash = source == NULL ? NULL : strrchr(source, '/');
        if (slash == NULL) {
            if (getcwd(working_directory, sizeof(working_directory)) == NULL) {
                ch_error_set(error, CH_ERROR_IO, "resolve current directory: %s", strerror(errno));
                return CH_ERROR_IO;
            }
            source = working_directory;
            base_length = strlen(source);
        } else {
            base_length = slash == source ? 1U : (size_t)(slash - source);
        }
        path_length = strlen(configured_path);
        joined = malloc(base_length + 1U + path_length + 1U);
        if (joined == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate resolved config path");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        memcpy(joined, source, base_length);
        joined[base_length] = '/';
        memcpy(joined + base_length + 1U, configured_path, path_length + 1U);
        *out_path = normalize_unix_path(joined);
        free(joined);
    }
    if (*out_path == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate resolved config path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static char *config_path_directory(const char *path) {
    const char *slash = strrchr(path, '/');
    size_t length;
    char *directory;
    if (slash == NULL) return ch_strdup(".");
    length = slash == path ? 1U : (size_t)(slash - path);
    directory = malloc(length + 1U);
    if (directory != NULL) {
        memcpy(directory, path, length);
        directory[length] = '\0';
    }
    return directory;
}

static const char *config_path_basename(const char *path) {
    const char *slash = strrchr(path, '/');
    return slash == NULL ? path : slash + 1;
}

static ch_status config_mkdir_all(const char *directory, ch_error *error) {
    char *copy = ch_strdup(directory);
    char *cursor;
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy config directory");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    cursor = copy + (copy[0] == '/' ? 1 : 0);
    for (;;) {
        char saved;
        while (*cursor != '\0' && *cursor != '/') ++cursor;
        saved = *cursor;
        *cursor = '\0';
        if (copy[0] != '\0' && strcmp(copy, ".") != 0 &&
            mkdir(copy, S_IRWXU) != 0 && errno != EEXIST) {
            ch_error_set(error, CH_ERROR_IO, "create config directory: %s", strerror(errno));
            free(copy);
            return CH_ERROR_IO;
        }
        if (saved == '\0') break;
        *cursor++ = saved;
        while (*cursor == '/') ++cursor;
    }
    free(copy);
    return CH_OK;
}

static ch_status config_write_all(int descriptor, const char *bytes, size_t length,
                                  ch_error *error, const char *operation) {
    size_t offset = 0U;
    while (offset < length) {
        ssize_t written = write(descriptor, bytes + offset, length - offset);
        if (written < 0) {
            if (errno == EINTR) continue;
            ch_error_set(error, CH_ERROR_IO, "%s: %s", operation, strerror(errno));
            return CH_ERROR_IO;
        }
        if (written == 0) {
            ch_error_set(error, CH_ERROR_IO, "%s: short write", operation);
            return CH_ERROR_IO;
        }
        offset += (size_t)written;
    }
    return CH_OK;
}

static char *config_unique_backup_path(const char *path, ch_error *error) {
    struct timespec now;
    int64_t timestamp;
    int required;
    char *candidate;
    struct stat info;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) {
        ch_error_set(error, CH_ERROR_IO, "read backup timestamp: %s", strerror(errno));
        return NULL;
    }
    timestamp = (int64_t)now.tv_sec * INT64_C(1000000000) + (int64_t)now.tv_nsec;
    required = snprintf(NULL, 0, "%s.%" PRId64 ".bak", path, timestamp);
    if (required < 0) return NULL;
    candidate = malloc((size_t)required + 1U);
    if (candidate == NULL) return NULL;
    for (int attempt = 0; attempt < 1024; ++attempt) {
        (void)snprintf(candidate, (size_t)required + 1U,
                       "%s.%" PRId64 ".bak", path, timestamp + attempt);
        if (lstat(candidate, &info) != 0) {
            if (errno == ENOENT) return candidate;
            ch_error_set(error, CH_ERROR_IO, "stat config backup: %s", strerror(errno));
            free(candidate);
            return NULL;
        }
    }
    ch_error_set(error, CH_ERROR_IO, "unable to allocate config backup path");
    free(candidate);
    return NULL;
}

typedef struct config_backup_entry {
    char *path;
    int64_t timestamp;
} config_backup_entry;

static int config_backup_compare(const void *left, const void *right) {
    const config_backup_entry *a = left;
    const config_backup_entry *b = right;
    if (a->timestamp < b->timestamp) return -1;
    if (a->timestamp > b->timestamp) return 1;
    return 0;
}

static void config_prune_backups(const char *path) {
    char *directory = config_path_directory(path);
    const char *basename = config_path_basename(path);
    DIR *stream;
    struct dirent *entry;
    config_backup_entry *backups = NULL;
    size_t count = 0U;
    size_t capacity = 0U;
    size_t prefix_length;
    if (directory == NULL) return;
    stream = opendir(directory);
    if (stream == NULL) { free(directory); return; }
    prefix_length = strlen(basename);
    while ((entry = readdir(stream)) != NULL) {
        size_t name_length = strlen(entry->d_name);
        char *end = NULL;
        int64_t timestamp;
        char *full_path;
        if (name_length <= prefix_length + 5U ||
            strncmp(entry->d_name, basename, prefix_length) != 0 ||
            entry->d_name[prefix_length] != '.' ||
            strcmp(entry->d_name + name_length - 4U, ".bak") != 0) continue;
        errno = 0;
        timestamp = strtoll(entry->d_name + prefix_length + 1U, &end, 10);
        if (errno != 0 || end != entry->d_name + name_length - 4U) continue;
        if (count == capacity) {
            size_t next_capacity = capacity == 0U ? 8U : capacity * 2U;
            config_backup_entry *next = realloc(backups, next_capacity * sizeof(*next));
            if (next == NULL) break;
            backups = next;
            capacity = next_capacity;
        }
        full_path = malloc(strlen(directory) + 1U + name_length + 1U);
        if (full_path == NULL) break;
        (void)sprintf(full_path, "%s/%s", directory, entry->d_name);
        backups[count++] = (config_backup_entry){.path = full_path, .timestamp = timestamp};
    }
    (void)closedir(stream);
    if (count > 1U) {
        qsort(backups, count, sizeof(*backups), config_backup_compare);
    }
    if (count > CH_CONFIG_MAX_BACKUPS) {
        for (size_t index = 0U; index < count - CH_CONFIG_MAX_BACKUPS; ++index) {
            (void)unlink(backups[index].path);
        }
    }
    for (size_t index = 0U; index < count; ++index) free(backups[index].path);
    free(backups);
    free(directory);
}

ch_status ch_config_write_atomic_document(const char *path, const char *toml,
                                          char **out_backup_path,
                                          ch_error *error) {
    ch_config *validated = NULL;
    char *directory = NULL;
    char *backup_path = NULL;
    char *temporary_path = NULL;
    struct stat existing;
    int descriptor = -1;
    ch_status status;
    size_t document_length;
    ch_error_clear(error);
    if (out_backup_path != NULL) *out_backup_path = NULL;
    if (path == NULL || path[0] == '\0' || toml == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config path and TOML are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    status = ch_config_parse(toml, path, &validated, error);
    if (status != CH_OK) return status;
    ch_config_free(validated);
    directory = config_path_directory(path);
    if (directory == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate config directory");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    status = config_mkdir_all(directory, error);
    if (status != CH_OK) goto cleanup;
    if (stat(path, &existing) == 0 && existing.st_size > 0) {
        FILE *source = fopen(path, "rb");
        if (source != NULL) {
            char buffer[16384];
            backup_path = config_unique_backup_path(path, error);
            if (backup_path == NULL) {
                (void)fclose(source);
                status = error == NULL || error->code == CH_OK
                    ? CH_ERROR_OUT_OF_MEMORY : error->code;
                goto cleanup;
            }
            descriptor = open(backup_path, O_WRONLY | O_CREAT | O_EXCL, S_IRUSR | S_IWUSR);
            if (descriptor < 0) {
                (void)fclose(source);
                ch_error_set(error, CH_ERROR_IO, "write config backup: %s", strerror(errno));
                status = CH_ERROR_IO;
                goto cleanup;
            }
            for (;;) {
                size_t count = fread(buffer, 1U, sizeof(buffer), source);
                if (count > 0U && config_write_all(descriptor, buffer, count, error,
                                                   "write config backup") != CH_OK) {
                    status = CH_ERROR_IO;
                    break;
                }
                if (count < sizeof(buffer)) {
                    if (ferror(source) != 0) {
                        ch_error_set(error, CH_ERROR_IO, "read existing config: %s", strerror(errno));
                        status = CH_ERROR_IO;
                    }
                    break;
                }
            }
            if (close(descriptor) != 0 && status == CH_OK) {
                ch_error_set(error, CH_ERROR_IO, "close config backup: %s", strerror(errno));
                status = CH_ERROR_IO;
            }
            descriptor = -1;
            (void)fclose(source);
            if (status != CH_OK) goto cleanup;
        }
    }
    temporary_path = malloc(strlen(directory) + sizeof("/.clambhook-XXXXXX"));
    if (temporary_path == NULL) {
        status = CH_ERROR_OUT_OF_MEMORY;
        ch_error_set(error, status, "allocate temporary config path");
        goto cleanup;
    }
    (void)sprintf(temporary_path, "%s/.clambhook-XXXXXX", directory);
    descriptor = mkstemp(temporary_path);
    if (descriptor < 0 || fchmod(descriptor, S_IRUSR | S_IWUSR) != 0) {
        status = CH_ERROR_IO;
        ch_error_set(error, status, "create temporary config: %s", strerror(errno));
        goto cleanup;
    }
    document_length = strlen(toml);
    status = config_write_all(descriptor, toml, document_length, error,
                              "write temporary config");
    if (status == CH_OK && fsync(descriptor) != 0) {
        status = CH_ERROR_IO;
        ch_error_set(error, status, "sync temporary config: %s", strerror(errno));
    }
    if (close(descriptor) != 0 && status == CH_OK) {
        status = CH_ERROR_IO;
        ch_error_set(error, status, "close temporary config: %s", strerror(errno));
    }
    descriptor = -1;
    if (status != CH_OK) goto cleanup;
    if (rename(temporary_path, path) != 0) {
        status = CH_ERROR_IO;
        ch_error_set(error, status, "replace config: %s", strerror(errno));
        goto cleanup;
    }
    temporary_path[0] = '\0';
    config_prune_backups(path);
    if (out_backup_path != NULL) {
        *out_backup_path = backup_path;
        backup_path = NULL;
    }
    status = CH_OK;

cleanup:
    if (descriptor >= 0) (void)close(descriptor);
    if (temporary_path != NULL && temporary_path[0] != '\0') (void)unlink(temporary_path);
    if (status != CH_OK && backup_path != NULL) {
        /* Keep a successfully written recovery point even when replacement fails. */
    }
    free(temporary_path);
    free(backup_path);
    free(directory);
    return status;
}

bool ch_config_table_has(const ch_config_table *table, const char *key) {
    return table != NULL && key != NULL && toml_key_exists(as_toml_table(table), key) != 0;
}

const ch_config_table *ch_config_table_get_table(const ch_config_table *table,
                                                  const char *key) {
    if (table == NULL || key == NULL) return NULL;
    return as_config_table(toml_table_in(as_toml_table(table), key));
}

const ch_config_array *ch_config_table_get_array(const ch_config_table *table,
                                                  const char *key) {
    if (table == NULL || key == NULL) return NULL;
    return as_config_array(toml_array_in(as_toml_table(table), key));
}

static ch_status missing_value(ch_error *error, const char *key) {
    ch_error_set(error, CH_ERROR_NOT_FOUND, "config value %s is missing or has the wrong type", key);
    return CH_ERROR_NOT_FOUND;
}

ch_status ch_config_table_get_string(const ch_config_table *table, const char *key,
                                     char **out_value, ch_error *error) {
    toml_datum_t value;
    ch_error_clear(error);
    if (table == NULL || key == NULL || out_value == NULL) return missing_value(error, key == NULL ? "" : key);
    *out_value = NULL;
    value = toml_string_in(as_toml_table(table), key);
    if (value.ok == 0) return missing_value(error, key);
    *out_value = value.u.s;
    return CH_OK;
}

ch_status ch_config_table_get_bool(const ch_config_table *table, const char *key,
                                   bool *out_value, ch_error *error) {
    toml_datum_t value;
    ch_error_clear(error);
    if (table == NULL || key == NULL || out_value == NULL) return missing_value(error, key == NULL ? "" : key);
    value = toml_bool_in(as_toml_table(table), key);
    if (value.ok == 0) return missing_value(error, key);
    *out_value = value.u.b != 0;
    return CH_OK;
}

ch_status ch_config_table_get_int(const ch_config_table *table, const char *key,
                                  int64_t *out_value, ch_error *error) {
    toml_datum_t value;
    ch_error_clear(error);
    if (table == NULL || key == NULL || out_value == NULL) return missing_value(error, key == NULL ? "" : key);
    value = toml_int_in(as_toml_table(table), key);
    if (value.ok == 0) return missing_value(error, key);
    *out_value = value.u.i;
    return CH_OK;
}

ch_status ch_config_table_get_double(const ch_config_table *table, const char *key,
                                     double *out_value, ch_error *error) {
    toml_datum_t value;
    ch_error_clear(error);
    if (table == NULL || key == NULL || out_value == NULL) return missing_value(error, key == NULL ? "" : key);
    value = toml_double_in(as_toml_table(table), key);
    if (value.ok != 0) {
        *out_value = value.u.d;
        return CH_OK;
    }
    {
        toml_datum_t integer = toml_int_in(as_toml_table(table), key);
        if (integer.ok == 0) return missing_value(error, key);
        *out_value = (double)integer.u.i;
    }
    return CH_OK;
}

size_t ch_config_array_count(const ch_config_array *array) {
    int count = array == NULL ? 0 : toml_array_nelem(as_toml_array(array));
    return count < 0 ? 0U : (size_t)count;
}

ch_config_array_kind ch_config_array_get_kind(const ch_config_array *array) {
    char kind = array == NULL ? 'm' : toml_array_kind(as_toml_array(array));
    if (kind == 'v') return CH_CONFIG_ARRAY_VALUES;
    if (kind == 't') return CH_CONFIG_ARRAY_TABLES;
    if (kind == 'a') return CH_CONFIG_ARRAY_ARRAYS;
    return CH_CONFIG_ARRAY_MIXED;
}

const ch_config_table *ch_config_array_get_table(const ch_config_array *array,
                                                  size_t index) {
    int converted;
    if (array == NULL || !size_to_index(index, &converted)) return NULL;
    return as_config_table(toml_table_at(as_toml_array(array), converted));
}

const ch_config_array *ch_config_array_get_array(const ch_config_array *array,
                                                  size_t index) {
    int converted;
    if (array == NULL || !size_to_index(index, &converted)) return NULL;
    return as_config_array(toml_array_at(as_toml_array(array), converted));
}

static ch_status array_missing(ch_error *error, size_t index) {
    ch_error_set(error, CH_ERROR_NOT_FOUND, "config array value %zu is missing or has the wrong type", index);
    return CH_ERROR_NOT_FOUND;
}

ch_status ch_config_array_get_string(const ch_config_array *array, size_t index,
                                     char **out_value, ch_error *error) {
    int converted;
    toml_datum_t value;
    ch_error_clear(error);
    if (array == NULL || out_value == NULL || !size_to_index(index, &converted)) return array_missing(error, index);
    *out_value = NULL;
    value = toml_string_at(as_toml_array(array), converted);
    if (value.ok == 0) return array_missing(error, index);
    *out_value = value.u.s;
    return CH_OK;
}

ch_status ch_config_array_get_bool(const ch_config_array *array, size_t index,
                                   bool *out_value, ch_error *error) {
    int converted;
    toml_datum_t value;
    ch_error_clear(error);
    if (array == NULL || out_value == NULL || !size_to_index(index, &converted)) return array_missing(error, index);
    value = toml_bool_at(as_toml_array(array), converted);
    if (value.ok == 0) return array_missing(error, index);
    *out_value = value.u.b != 0;
    return CH_OK;
}

ch_status ch_config_array_get_int(const ch_config_array *array, size_t index,
                                  int64_t *out_value, ch_error *error) {
    int converted;
    toml_datum_t value;
    ch_error_clear(error);
    if (array == NULL || out_value == NULL || !size_to_index(index, &converted)) return array_missing(error, index);
    value = toml_int_at(as_toml_array(array), converted);
    if (value.ok == 0) return array_missing(error, index);
    *out_value = value.u.i;
    return CH_OK;
}

ch_status ch_config_array_get_double(const ch_config_array *array, size_t index,
                                     double *out_value, ch_error *error) {
    int converted;
    toml_datum_t value;
    ch_error_clear(error);
    if (array == NULL || out_value == NULL || !size_to_index(index, &converted)) return array_missing(error, index);
    value = toml_double_at(as_toml_array(array), converted);
    if (value.ok != 0) {
        *out_value = value.u.d;
        return CH_OK;
    }
    {
        toml_datum_t integer = toml_int_at(as_toml_array(array), converted);
        if (integer.ok == 0) return array_missing(error, index);
        *out_value = (double)integer.u.i;
    }
    return CH_OK;
}

static bool config_json_table(ch_json_buffer *json, const toml_table_t *table,
                              unsigned int depth);
static bool config_json_array(ch_json_buffer *json, const toml_array_t *array,
                              unsigned int depth);

static bool config_json_table_value(ch_json_buffer *json, const toml_table_t *table,
                                    const char *key, unsigned int depth) {
    const toml_table_t *child_table = toml_table_in(table, key);
    const toml_array_t *child_array;
    toml_datum_t value;
    if (child_table != NULL) return config_json_table(json, child_table, depth + 1U);
    child_array = toml_array_in(table, key);
    if (child_array != NULL) return config_json_array(json, child_array, depth + 1U);
    value = toml_string_in(table, key);
    if (value.ok != 0) {
        bool result = ch_json_append_string(json, value.u.s);
        free(value.u.s);
        return result;
    }
    value = toml_bool_in(table, key);
    if (value.ok != 0) return ch_json_append(json, value.u.b != 0 ? "true" : "false");
    value = toml_int_in(table, key);
    if (value.ok != 0) return ch_json_append_format(json, "%" PRId64, value.u.i);
    value = toml_double_in(table, key);
    if (value.ok != 0 && isfinite(value.u.d)) {
        return ch_json_append_format(json, "%.17g", value.u.d);
    }
    return ch_json_append(json, "null");
}

static bool config_json_array_value(ch_json_buffer *json, const toml_array_t *array,
                                    int index, unsigned int depth) {
    const toml_table_t *child_table = toml_table_at(array, index);
    const toml_array_t *child_array;
    toml_datum_t value;
    if (child_table != NULL) return config_json_table(json, child_table, depth + 1U);
    child_array = toml_array_at(array, index);
    if (child_array != NULL) return config_json_array(json, child_array, depth + 1U);
    value = toml_string_at(array, index);
    if (value.ok != 0) {
        bool result = ch_json_append_string(json, value.u.s);
        free(value.u.s);
        return result;
    }
    value = toml_bool_at(array, index);
    if (value.ok != 0) return ch_json_append(json, value.u.b != 0 ? "true" : "false");
    value = toml_int_at(array, index);
    if (value.ok != 0) return ch_json_append_format(json, "%" PRId64, value.u.i);
    value = toml_double_at(array, index);
    if (value.ok != 0 && isfinite(value.u.d)) {
        return ch_json_append_format(json, "%.17g", value.u.d);
    }
    return ch_json_append(json, "null");
}

static bool config_json_table(ch_json_buffer *json, const toml_table_t *table,
                              unsigned int depth) {
    if (table == NULL || depth > 64U || !ch_json_append(json, "{")) return false;
    for (int index = 0;; ++index) {
        const char *key = toml_key_in(table, index);
        if (key == NULL) break;
        if ((index > 0 && !ch_json_append(json, ",")) ||
            !ch_json_append_string(json, key) || !ch_json_append(json, ":") ||
            !config_json_table_value(json, table, key, depth)) return false;
    }
    return ch_json_append(json, "}");
}

static bool config_json_array(ch_json_buffer *json, const toml_array_t *array,
                              unsigned int depth) {
    int count;
    if (array == NULL || depth > 64U || !ch_json_append(json, "[")) return false;
    count = toml_array_nelem(array);
    for (int index = 0; index < count; ++index) {
        if ((index > 0 && !ch_json_append(json, ",")) ||
            !config_json_array_value(json, array, index, depth)) return false;
    }
    return ch_json_append(json, "]");
}

ch_status ch_config_table_json(const ch_config_table *table, char **out_json,
                               ch_error *error) {
    ch_json_buffer json;
    ch_error_clear(error);
    if (table == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config table and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    ch_json_init(&json);
    if (!config_json_table(&json, as_toml_table(table), 0U)) {
        ch_json_dispose(&json);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode config table JSON");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *out_json = ch_json_take(&json);
    return CH_OK;
}

ch_status ch_config_array_json(const ch_config_array *array, char **out_json,
                               ch_error *error) {
    ch_json_buffer json;
    ch_error_clear(error);
    if (array == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config array and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    ch_json_init(&json);
    if (!config_json_array(&json, as_toml_array(array), 0U)) {
        ch_json_dispose(&json);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode config array JSON");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *out_json = ch_json_take(&json);
    return CH_OK;
}

ch_status ch_config_parse_duration_ns(const char *text, int64_t *out_nanoseconds,
                                      ch_error *error) {
    const char *cursor;
    long double total = 0.0L;
    bool negative = false;
    bool consumed = false;
    ch_error_clear(error);
    if (text == NULL || out_nanoseconds == NULL || text[0] == '\0') {
        ch_error_set(error, CH_ERROR_PARSE, "duration is required");
        return CH_ERROR_PARSE;
    }
    cursor = text;
    if (*cursor == '+' || *cursor == '-') {
        negative = *cursor == '-';
        ++cursor;
    }
    if (strcmp(cursor, "0") == 0) {
        *out_nanoseconds = 0;
        return CH_OK;
    }
    while (*cursor != '\0') {
        char *number_end = NULL;
        long double number;
        long double multiplier;
        errno = 0;
        number = strtold(cursor, &number_end);
        if (errno != 0 || number_end == cursor || number < 0.0L || !isfinite((double)number)) {
            ch_error_set(error, CH_ERROR_PARSE, "invalid duration %s", text);
            return CH_ERROR_PARSE;
        }
        cursor = number_end;
        if (strncmp(cursor, "ns", 2U) == 0) { multiplier = 1.0L; cursor += 2; }
        else if (strncmp(cursor, "us", 2U) == 0) { multiplier = 1000.0L; cursor += 2; }
        else if (strncmp(cursor, "ms", 2U) == 0) { multiplier = 1000000.0L; cursor += 2; }
        else if (*cursor == 's') { multiplier = 1000000000.0L; ++cursor; }
        else if (*cursor == 'm') { multiplier = 60000000000.0L; ++cursor; }
        else if (*cursor == 'h') { multiplier = 3600000000000.0L; ++cursor; }
        else {
            ch_error_set(error, CH_ERROR_PARSE, "invalid duration unit in %s", text);
            return CH_ERROR_PARSE;
        }
        total += number * multiplier;
        consumed = true;
        if (total > (long double)INT64_MAX) {
            ch_error_set(error, CH_ERROR_PARSE, "duration overflows int64: %s", text);
            return CH_ERROR_PARSE;
        }
    }
    if (!consumed) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid duration %s", text);
        return CH_ERROR_PARSE;
    }
    *out_nanoseconds = (int64_t)(negative ? -total : total);
    return CH_OK;
}
