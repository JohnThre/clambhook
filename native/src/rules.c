#include "clambhook/rules.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <uv.h>

#include "internal.h"

typedef struct ch_owned_strings {
    char **items;
    size_t count;
} ch_owned_strings;

typedef struct ch_cidr {
    int family;
    uint8_t address[16];
    unsigned prefix_length;
    char *text;
} ch_cidr;

typedef struct ch_cidr_list {
    ch_cidr *items;
    size_t count;
} ch_cidr_list;

typedef struct ch_compiled_rule_set {
    char *name;
    ch_owned_strings domains;
    ch_owned_strings suffixes;
    ch_owned_strings keywords;
    ch_cidr_list cidrs;
} ch_compiled_rule_set;

typedef struct ch_compiled_rule {
    char *name;
    char *action;
    char *chain_name;
    char *group_name;
    ch_owned_strings domains;
    ch_owned_strings suffixes;
    ch_owned_strings keywords;
    ch_cidr_list cidrs;
    ch_cidr_list source_cidrs;
    int *ports;
    size_t port_count;
    ch_owned_strings networks;
    ch_owned_strings processes;
    ch_compiled_rule_set *rule_sets;
    size_t rule_set_count;
} ch_compiled_rule;

struct ch_rule_engine {
    char *default_chain;
    ch_compiled_rule *rules;
    size_t rule_count;
};

typedef struct ch_match {
    const char *kind;
    const char *value;
    char storage[512];
} ch_match;

static char *ch_trimmed_copy(const char *value) {
    if (value == NULL) {
        return ch_strdup("");
    }
    while (isspace((unsigned char)*value)) {
        ++value;
    }
    const char *end = value + strlen(value);
    while (end > value && isspace((unsigned char)end[-1])) {
        --end;
    }
    size_t length = (size_t)(end - value);
    char *result = malloc(length + 1U);
    if (result != NULL) {
        memcpy(result, value, length);
        result[length] = '\0';
    }
    return result;
}

static char *ch_trimmed_lower(const char *value, int strip_dot) {
    if (value == NULL) {
        return ch_strdup("");
    }
    while (isspace((unsigned char)*value)) {
        ++value;
    }
    const char *end = value + strlen(value);
    while (end > value && isspace((unsigned char)end[-1])) {
        --end;
    }
    if (strip_dot && value < end && *value == '.') {
        ++value;
    }
    size_t length = (size_t)(end - value);
    char *result = malloc(length + 1U);
    if (result == NULL) {
        return NULL;
    }
    for (size_t index = 0U; index < length; ++index) {
        result[index] = (char)tolower((unsigned char)value[index]);
    }
    result[length] = '\0';
    return result;
}

static void ch_owned_strings_clear(ch_owned_strings *strings) {
    for (size_t index = 0U; index < strings->count; ++index) {
        free(strings->items[index]);
    }
    free(strings->items);
    memset(strings, 0, sizeof(*strings));
}

static int ch_owned_strings_contains(const ch_owned_strings *strings, const char *value) {
    for (size_t index = 0U; index < strings->count; ++index) {
        if (strcmp(strings->items[index], value) == 0) {
            return 1;
        }
    }
    return 0;
}

static ch_status ch_owned_strings_compile(
    ch_string_list input,
    int strip_dot,
    ch_owned_strings *output,
    ch_error *error
) {
    memset(output, 0, sizeof(*output));
    if (input.count == 0U) {
        return CH_OK;
    }
    if (input.items == NULL || input.count > SIZE_MAX / sizeof(char *)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "invalid string list");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    output->items = calloc(input.count, sizeof(*output->items));
    if (output->items == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate normalized strings");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < input.count; ++index) {
        char *normalized = ch_trimmed_lower(input.items[index], strip_dot);
        if (normalized == NULL) {
            ch_owned_strings_clear(output);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "normalize string");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (normalized[0] == '\0' || ch_owned_strings_contains(output, normalized)) {
            free(normalized);
            continue;
        }
        output->items[output->count++] = normalized;
    }
    return CH_OK;
}

static int ch_name_known(ch_string_list names, const char *name) {
    for (size_t index = 0U; index < names.count; ++index) {
        if (names.items[index] != NULL && strcmp(names.items[index], name) == 0) {
            return 1;
        }
    }
    return 0;
}

static void ch_cidr_list_clear(ch_cidr_list *list) {
    for (size_t index = 0U; index < list->count; ++index) {
        free(list->items[index].text);
    }
    free(list->items);
    memset(list, 0, sizeof(*list));
}

static ch_status ch_cidr_list_compile(ch_string_list input, ch_cidr_list *output, ch_error *error) {
    memset(output, 0, sizeof(*output));
    if (input.count == 0U) {
        return CH_OK;
    }
    if (input.items == NULL || input.count > SIZE_MAX / sizeof(ch_cidr)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "invalid CIDR list");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    output->items = calloc(input.count, sizeof(*output->items));
    if (output->items == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate CIDR list");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < input.count; ++index) {
        const char *raw = input.items[index] == NULL ? "" : input.items[index];
        char *copy = ch_trimmed_lower(raw, 0);
        if (copy == NULL) {
            ch_cidr_list_clear(output);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy CIDR");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        char *slash = strrchr(copy, '/');
        if (slash == NULL || slash == copy || slash[1] == '\0') {
            free(copy);
            ch_cidr_list_clear(output);
            ch_error_set(error, CH_ERROR_PARSE, "invalid CIDR %s", raw);
            return CH_ERROR_PARSE;
        }
        *slash = '\0';
        errno = 0;
        char *prefix_end = NULL;
        long prefix = strtol(slash + 1, &prefix_end, 10);
        ch_cidr *cidr = &output->items[output->count];
        if (inet_pton(AF_INET, copy, cidr->address) == 1) {
            cidr->family = AF_INET;
            if (prefix < 0L || prefix > 32L) {
                errno = EINVAL;
            }
        } else if (inet_pton(AF_INET6, copy, cidr->address) == 1) {
            cidr->family = AF_INET6;
            if (prefix < 0L || prefix > 128L) {
                errno = EINVAL;
            }
        } else {
            errno = EINVAL;
        }
        *slash = '/';
        if (errno != 0 || prefix_end == slash + 1 || *prefix_end != '\0') {
            free(copy);
            ch_cidr_list_clear(output);
            ch_error_set(error, CH_ERROR_PARSE, "invalid CIDR %s", raw);
            return CH_ERROR_PARSE;
        }
        cidr->prefix_length = (unsigned)prefix;
        cidr->text = copy;
        ++output->count;
    }
    return CH_OK;
}

static int ch_cidr_contains(const ch_cidr *cidr, const uint8_t *address) {
    unsigned whole = cidr->prefix_length / 8U;
    unsigned remainder = cidr->prefix_length % 8U;
    if (whole > 0U && memcmp(cidr->address, address, whole) != 0) {
        return 0;
    }
    if (remainder == 0U) {
        return 1;
    }
    uint8_t mask = (uint8_t)(0xffU << (8U - remainder));
    return (cidr->address[whole] & mask) == (address[whole] & mask);
}

static const char *ch_match_cidrs(const ch_cidr_list *list, const char *raw_host) {
    char host[INET6_ADDRSTRLEN + 2U];
    size_t length = strlen(raw_host == NULL ? "" : raw_host);
    if (length >= sizeof(host)) {
        return NULL;
    }
    memcpy(host, raw_host == NULL ? "" : raw_host, length + 1U);
    if (host[0] == '[' && length > 1U && host[length - 1U] == ']') {
        memmove(host, host + 1, length - 2U);
        host[length - 2U] = '\0';
    }
    uint8_t address[16];
    int family = inet_pton(AF_INET, host, address) == 1 ? AF_INET : AF_UNSPEC;
    if (family == AF_UNSPEC && inet_pton(AF_INET6, host, address) == 1) {
        family = AF_INET6;
    }
    if (family == AF_UNSPEC) {
        return NULL;
    }
    for (size_t index = 0U; index < list->count; ++index) {
        if (list->items[index].family == family && ch_cidr_contains(&list->items[index], address)) {
            return list->items[index].text;
        }
    }
    return NULL;
}

static void ch_compiled_rule_set_clear(ch_compiled_rule_set *set) {
    free(set->name);
    ch_owned_strings_clear(&set->domains);
    ch_owned_strings_clear(&set->suffixes);
    ch_owned_strings_clear(&set->keywords);
    ch_cidr_list_clear(&set->cidrs);
    memset(set, 0, sizeof(*set));
}

static ch_status ch_compile_rule_set(
    const ch_rule_set_spec *input,
    ch_compiled_rule_set *output,
    ch_error *error
) {
    memset(output, 0, sizeof(*output));
    output->name = ch_strdup(input->name == NULL ? "" : input->name);
    if (output->name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule set name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_owned_strings_compile(input->domains, 0, &output->domains, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->domain_suffixes, 1, &output->suffixes, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->domain_keywords, 0, &output->keywords, error);
    if (status == CH_OK) status = ch_cidr_list_compile(input->cidrs, &output->cidrs, error);
    if (status != CH_OK) {
        ch_compiled_rule_set_clear(output);
    }
    return status;
}

static void ch_compiled_rule_clear(ch_compiled_rule *rule) {
    free(rule->name);
    free(rule->action);
    free(rule->chain_name);
    free(rule->group_name);
    ch_owned_strings_clear(&rule->domains);
    ch_owned_strings_clear(&rule->suffixes);
    ch_owned_strings_clear(&rule->keywords);
    ch_cidr_list_clear(&rule->cidrs);
    ch_cidr_list_clear(&rule->source_cidrs);
    free(rule->ports);
    ch_owned_strings_clear(&rule->networks);
    ch_owned_strings_clear(&rule->processes);
    for (size_t index = 0U; index < rule->rule_set_count; ++index) {
        ch_compiled_rule_set_clear(&rule->rule_sets[index]);
    }
    free(rule->rule_sets);
    memset(rule, 0, sizeof(*rule));
}

static ch_status ch_parse_action(
    const char *raw,
    ch_string_list known_chains,
    ch_string_list known_groups,
    ch_compiled_rule *output,
    ch_error *error
) {
    char *trimmed = ch_trimmed_copy(raw);
    char *normalized = ch_trimmed_lower(raw, 0);
    if (trimmed == NULL || normalized == NULL) {
        free(trimmed);
        free(normalized);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule action");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *normalized_target = strchr(normalized, ':');
    char *target = strchr(trimmed, ':');
    if (normalized_target != NULL) {
        *normalized_target = '\0';
    }
    if (target != NULL) {
        *target++ = '\0';
        while (isspace((unsigned char)*target)) {
            ++target;
        }
        char *target_end = target + strlen(target);
        while (target_end > target && isspace((unsigned char)target_end[-1])) {
            *--target_end = '\0';
        }
    }
    if (strcmp(normalized, CH_RULE_ACTION_DIRECT) == 0 ||
        strcmp(normalized, CH_RULE_ACTION_BLOCK) == 0 ||
        strcmp(normalized, CH_RULE_ACTION_REJECT) == 0) {
        if (target != NULL) {
            free(trimmed);
            free(normalized);
            ch_error_set(error, CH_ERROR_PARSE, "unknown action %s", raw == NULL ? "" : raw);
            return CH_ERROR_PARSE;
        }
        output->action = ch_strdup(normalized);
    } else if (strcmp(normalized, CH_RULE_ACTION_CHAIN) == 0 && target != NULL && *target != '\0') {
        if (!ch_name_known(known_chains, target)) {
            ch_error_set(error, CH_ERROR_NOT_FOUND, "chain %s not found", target);
            free(trimmed);
            free(normalized);
            return CH_ERROR_NOT_FOUND;
        }
        output->action = ch_strdup(CH_RULE_ACTION_CHAIN);
        output->chain_name = ch_strdup(target);
    } else if (strcmp(normalized, CH_RULE_ACTION_GROUP) == 0 && target != NULL && *target != '\0') {
        if (!ch_name_known(known_groups, target)) {
            ch_error_set(error, CH_ERROR_NOT_FOUND, "policy group %s not found", target);
            free(trimmed);
            free(normalized);
            return CH_ERROR_NOT_FOUND;
        }
        output->action = ch_strdup(CH_RULE_ACTION_GROUP);
        output->group_name = ch_strdup(target);
    } else {
        ch_error_set(error, CH_ERROR_PARSE, "unknown action %s", raw == NULL ? "" : raw);
        free(trimmed);
        free(normalized);
        return CH_ERROR_PARSE;
    }
    free(trimmed);
    free(normalized);
    if (output->action == NULL ||
        (strcmp(output->action, CH_RULE_ACTION_CHAIN) == 0 && output->chain_name == NULL) ||
        (strcmp(output->action, CH_RULE_ACTION_GROUP) == 0 && output->group_name == NULL)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule action");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static const ch_rule_set_spec *ch_find_rule_set(
    const ch_rule_set_spec *sets,
    size_t count,
    const char *name
) {
    for (size_t index = 0U; index < count; ++index) {
        if (sets[index].name != NULL && strcmp(sets[index].name, name) == 0) {
            return &sets[index];
        }
    }
    return NULL;
}

static ch_status ch_compile_rule(
    const ch_rule_spec *input,
    ch_string_list known_chains,
    ch_string_list known_groups,
    const ch_rule_set_spec *known_rule_sets,
    size_t known_rule_set_count,
    ch_compiled_rule *output,
    ch_error *error
) {
    memset(output, 0, sizeof(*output));
    char *trimmed_name = ch_trimmed_copy(input->name);
    if (trimmed_name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    output->name = trimmed_name[0] == '\0' ? ch_strdup("unnamed") : trimmed_name;
    if (output->name != trimmed_name) {
        free(trimmed_name);
    }
    if (output->name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_parse_action(input->action, known_chains, known_groups, output, error);
    if (status != CH_OK) goto fail;
    if (input->rule_sets.count > 0U &&
        (input->domains.count > 0U || input->domain_suffixes.count > 0U ||
         input->domain_keywords.count > 0U || input->cidrs.count > 0U)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "rule_sets cannot be combined with destination matchers");
        status = CH_ERROR_INVALID_ARGUMENT;
        goto fail;
    }
    status = ch_owned_strings_compile(input->domains, 0, &output->domains, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->domain_suffixes, 1, &output->suffixes, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->domain_keywords, 0, &output->keywords, error);
    if (status == CH_OK) status = ch_cidr_list_compile(input->cidrs, &output->cidrs, error);
    if (status == CH_OK) status = ch_cidr_list_compile(input->source_cidrs, &output->source_cidrs, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->networks, 0, &output->networks, error);
    if (status == CH_OK) status = ch_owned_strings_compile(input->processes, 0, &output->processes, error);
    if (status != CH_OK) goto fail;
    if (input->ports.count > 0U) {
        output->ports = malloc(input->ports.count * sizeof(*output->ports));
        if (output->ports == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule ports");
            status = CH_ERROR_OUT_OF_MEMORY;
            goto fail;
        }
        memcpy(output->ports, input->ports.items, input->ports.count * sizeof(*output->ports));
        output->port_count = input->ports.count;
    }
    if (input->rule_sets.count > 0U) {
        output->rule_sets = calloc(input->rule_sets.count, sizeof(*output->rule_sets));
        if (output->rule_sets == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate rule set references");
            status = CH_ERROR_OUT_OF_MEMORY;
            goto fail;
        }
        for (size_t index = 0U; index < input->rule_sets.count; ++index) {
            char *name = ch_trimmed_copy(input->rule_sets.items[index]);
            if (name == NULL) {
                status = CH_ERROR_OUT_OF_MEMORY;
                ch_error_set(error, status, "copy rule set reference");
                goto fail;
            }
            if (name[0] == '\0') {
                free(name);
                continue;
            }
            int duplicate = 0;
            for (size_t prior = 0U; prior < output->rule_set_count; ++prior) {
                if (strcmp(output->rule_sets[prior].name, name) == 0) duplicate = 1;
            }
            if (duplicate) {
                free(name);
                continue;
            }
            const ch_rule_set_spec *set = ch_find_rule_set(known_rule_sets, known_rule_set_count, name);
            if (set == NULL) {
                ch_error_set(error, CH_ERROR_NOT_FOUND, "rule set %s not found", name);
                free(name);
                status = CH_ERROR_NOT_FOUND;
                goto fail;
            }
            free(name);
            status = ch_compile_rule_set(set, &output->rule_sets[output->rule_set_count], error);
            if (status != CH_OK) goto fail;
            ++output->rule_set_count;
        }
    }
    return CH_OK;

fail:
    ch_compiled_rule_clear(output);
    return status;
}

ch_rule_engine *ch_rule_engine_compile(
    const ch_rule_spec *rules,
    size_t rule_count,
    const char *default_chain,
    ch_string_list known_chains,
    ch_string_list known_groups,
    const ch_rule_set_spec *known_rule_sets,
    size_t known_rule_set_count,
    ch_error *error
) {
    ch_error_clear(error);
    char *normalized_default = ch_trimmed_copy(default_chain);
    if (normalized_default == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy default chain");
        return NULL;
    }
    if (normalized_default[0] == '\0') {
        free(normalized_default);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "rules: default chain is required");
        return NULL;
    }
    if (!ch_name_known(known_chains, normalized_default)) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "rules: default chain %s not found", normalized_default);
        free(normalized_default);
        return NULL;
    }
    if (rule_count > 0U && rules == NULL) {
        free(normalized_default);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "rules are required");
        return NULL;
    }
    ch_rule_engine *engine = calloc(1U, sizeof(*engine));
    if (engine == NULL) {
        free(normalized_default);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate rule engine");
        return NULL;
    }
    engine->default_chain = normalized_default;
    if (rule_count > 0U) {
        engine->rules = calloc(rule_count, sizeof(*engine->rules));
        if (engine->rules == NULL) {
            ch_rule_engine_destroy(engine);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate compiled rules");
            return NULL;
        }
    }
    for (size_t index = 0U; index < rule_count; ++index) {
        ch_status status = ch_compile_rule(
            &rules[index], known_chains, known_groups,
            known_rule_sets, known_rule_set_count,
            &engine->rules[index], error
        );
        if (status != CH_OK) {
            engine->rule_count = index + 1U;
            ch_rule_engine_destroy(engine);
            return NULL;
        }
        ++engine->rule_count;
    }
    return engine;
}

void ch_rule_engine_destroy(ch_rule_engine *engine) {
    if (engine == NULL) return;
    for (size_t index = 0U; index < engine->rule_count; ++index) {
        ch_compiled_rule_clear(&engine->rules[index]);
    }
    free(engine->rules);
    free(engine->default_chain);
    free(engine);
}

static void ch_normalize_host(char *host) {
    char *start = host;
    while (isspace((unsigned char)*start)) ++start;
    if (start != host) memmove(host, start, strlen(start) + 1U);
    size_t length = strlen(host);
    while (length > 0U && isspace((unsigned char)host[length - 1U])) host[--length] = '\0';
    if (length >= 2U && host[0] == '[' && host[length - 1U] == ']') {
        memmove(host, host + 1, length - 2U);
        host[length - 2U] = '\0';
        length -= 2U;
    }
    if (length > 0U && host[length - 1U] == '.') host[--length] = '\0';
    for (size_t index = 0U; index < length; ++index) {
        host[index] = (char)tolower((unsigned char)host[index]);
    }
}

static void ch_split_target(const char *target, char **host_out, char **port_out) {
    char *host = ch_strdup(target == NULL ? "" : target);
    char *port = ch_strdup("");
    if (host == NULL || port == NULL) {
        free(host); free(port); *host_out = NULL; *port_out = NULL; return;
    }
    char *start = host;
    while (isspace((unsigned char)*start)) ++start;
    if (start != host) memmove(host, start, strlen(start) + 1U);
    size_t length = strlen(host);
    while (length > 0U && isspace((unsigned char)host[length - 1U])) host[--length] = '\0';
    char *separator = NULL;
    if (host[0] == '[') {
        char *closing = strrchr(host, ']');
        if (closing != NULL && closing[1] == ':' && closing[2] != '\0') separator = closing + 1;
    } else {
        separator = strrchr(host, ':');
    }
    if (separator != NULL && separator[1] != '\0') {
        char *end = NULL;
        errno = 0;
        (void)strtol(separator + 1, &end, 10);
        if (errno == 0 && end != separator + 1 && *end == '\0') {
            free(port);
            port = ch_strdup(separator + 1);
            *separator = '\0';
        }
    }
    ch_normalize_host(host);
    *host_out = host;
    *port_out = port;
}

static int ch_domain_match(
    const ch_owned_strings *domains,
    const ch_owned_strings *suffixes,
    const ch_owned_strings *keywords,
    const char *host,
    ch_match *match
) {
    if (ch_owned_strings_contains(domains, host)) {
        match->kind = "domain"; match->value = host; return 1;
    }
    for (size_t index = 0U; index < suffixes->count; ++index) {
        const char *suffix = suffixes->items[index];
        size_t host_length = strlen(host), suffix_length = strlen(suffix);
        if (strcmp(host, suffix) == 0 ||
            (host_length > suffix_length &&
             host[host_length - suffix_length - 1U] == '.' &&
             strcmp(host + host_length - suffix_length, suffix) == 0)) {
            match->kind = "domain_suffix"; match->value = suffix; return 1;
        }
    }
    for (size_t index = 0U; index < keywords->count; ++index) {
        if (strstr(host, keywords->items[index]) != NULL) {
            match->kind = "domain_keyword"; match->value = keywords->items[index]; return 1;
        }
    }
    return 0;
}

static int ch_process_match(const ch_owned_strings *patterns, const char *name, const char *path, const char **value) {
    char *normalized_name = ch_trimmed_lower(name, 0);
    char *normalized_path = ch_trimmed_lower(path, 0);
    if (normalized_name == NULL || normalized_path == NULL) {
        free(normalized_name); free(normalized_path); return 0;
    }
    const char *base = strrchr(normalized_path, '/');
    base = base == NULL ? normalized_path : base + 1;
    int matched = 0;
    for (size_t index = 0U; index < patterns->count; ++index) {
        const char *pattern = patterns->items[index];
        if (strcmp(pattern, normalized_name) == 0 ||
            (normalized_path[0] != '\0' && strcmp(pattern, normalized_path) == 0) ||
            (base[0] != '\0' && strcmp(pattern, base) == 0)) {
            *value = pattern; matched = 1; break;
        }
    }
    free(normalized_name); free(normalized_path); return matched;
}

static int ch_rule_matches(
    const ch_compiled_rule *rule,
    const ch_rule_match_context *context,
    const char *network,
    const char *host,
    const char *port,
    ch_match *match
) {
    memset(match, 0, sizeof(*match));
    if (rule->networks.count > 0U) {
        if (!ch_owned_strings_contains(&rule->networks, network)) return 0;
        match->kind = "network"; match->value = network;
    }
    if (rule->port_count > 0U) {
        char *end = NULL; long number = strtol(port, &end, 10); int found = 0;
        if (end == port || *end != '\0') return 0;
        for (size_t index = 0U; index < rule->port_count; ++index) if (rule->ports[index] == number) found = 1;
        if (!found) return 0;
        if (match->kind == NULL) { match->kind = "port"; match->value = port; }
    }
    if (rule->source_cidrs.count > 0U) {
        char *source_host = NULL, *source_port = NULL;
        ch_split_target(context->source, &source_host, &source_port);
        const char *cidr = source_host == NULL ? NULL : ch_match_cidrs(&rule->source_cidrs, source_host);
        free(source_host); free(source_port);
        if (cidr == NULL) return 0;
        if (match->kind == NULL) { match->kind = "source_cidr"; match->value = cidr; }
    }
    if (rule->processes.count > 0U) {
        const char *process = NULL;
        if (!ch_process_match(&rule->processes, context->process_name, context->process_path, &process)) return 0;
        if (match->kind == NULL) { match->kind = "process"; match->value = process; }
    }
    if (rule->domains.count + rule->suffixes.count + rule->keywords.count > 0U) {
        ch_match domain = {0};
        if (!ch_domain_match(&rule->domains, &rule->suffixes, &rule->keywords, host, &domain)) return 0;
        if (match->kind == NULL) *match = domain;
    }
    if (rule->cidrs.count > 0U) {
        const char *cidr = ch_match_cidrs(&rule->cidrs, host);
        if (cidr == NULL) return 0;
        if (match->kind == NULL) { match->kind = "cidr"; match->value = cidr; }
    }
    if (rule->rule_set_count > 0U) {
        int found = 0;
        for (size_t index = 0U; index < rule->rule_set_count && !found; ++index) {
            const ch_compiled_rule_set *set = &rule->rule_sets[index];
            ch_match nested = {0};
            if (ch_domain_match(&set->domains, &set->suffixes, &set->keywords, host, &nested)) {
                (void)snprintf(match->storage, sizeof(match->storage), "%s:%s", set->name, nested.value);
                match->kind = nested.kind[0] == 'd' ?
                    (strcmp(nested.kind, "domain") == 0 ? "rule_set_domain" :
                     strcmp(nested.kind, "domain_suffix") == 0 ? "rule_set_domain_suffix" : "rule_set_domain_keyword") :
                    "rule_set";
                match->value = match->storage; found = 1;
            } else {
                const char *cidr = ch_match_cidrs(&set->cidrs, host);
                if (cidr != NULL) {
                    (void)snprintf(match->storage, sizeof(match->storage), "%s:%s", set->name, cidr);
                    match->kind = "rule_set_cidr"; match->value = match->storage; found = 1;
                }
            }
        }
        if (!found) return 0;
    }
    if (match->kind == NULL) { match->kind = "all_traffic"; match->value = "*"; }
    return 1;
}

static int ch_decision_copy(char **destination, const char *source) {
    *destination = ch_strdup(source == NULL ? "" : source);
    return *destination != NULL;
}

void ch_rule_decision_clear(ch_rule_decision *decision) {
    if (decision == NULL) return;
    free(decision->rule_name); free(decision->action); free(decision->chain_name);
    free(decision->group_name); free(decision->target); free(decision->host);
    free(decision->port); free(decision->network); free(decision->source);
    free(decision->matcher_kind); free(decision->matcher_value); free(decision->summary);
    memset(decision, 0, sizeof(*decision));
}

ch_status ch_rule_engine_decide(
    const ch_rule_engine *engine,
    const ch_rule_match_context *context,
    ch_rule_decision *decision,
    ch_error *error
) {
    ch_error_clear(error);
    if (engine == NULL || context == NULL || decision == NULL || context->target == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "rule engine, context, target, and decision are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(decision, 0, sizeof(*decision));
    uint64_t started = uv_hrtime();
    char *host = NULL, *port = NULL;
    char *network = ch_trimmed_lower(context->network, 0);
    ch_split_target(context->target, &host, &port);
    if (host == NULL || port == NULL || network == NULL) {
        free(host); free(port); free(network);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "prepare rule decision");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    const ch_compiled_rule *selected = NULL;
    ch_match match = {0};
    size_t number = engine->rule_count + 1U;
    for (size_t index = 0U; index < engine->rule_count; ++index) {
        if (ch_rule_matches(&engine->rules[index], context, network, host, port, &match)) {
            selected = &engine->rules[index]; number = index + 1U; break;
        }
    }
    const char *rule_name = selected == NULL ? "" : selected->name;
    const char *action = selected == NULL ? CH_RULE_ACTION_CHAIN : selected->action;
    const char *chain = selected == NULL ? engine->default_chain : selected->chain_name;
    const char *group = selected == NULL ? "" : selected->group_name;
    char summary[768];
    if (selected == NULL) {
        (void)snprintf(summary, sizeof(summary), "No rule matched; used the default chain.");
    } else if (match.value == NULL || match.value[0] == '\0') {
        (void)snprintf(summary, sizeof(summary), "Rule \"%s\" matched %s.", rule_name, match.kind);
    } else {
        (void)snprintf(summary, sizeof(summary), "Rule \"%s\" matched %s \"%s\".", rule_name, match.kind, match.value);
    }
    int copied = ch_decision_copy(&decision->rule_name, rule_name) &&
        ch_decision_copy(&decision->action, action) && ch_decision_copy(&decision->chain_name, chain) &&
        ch_decision_copy(&decision->group_name, group) && ch_decision_copy(&decision->target, context->target) &&
        ch_decision_copy(&decision->host, host) && ch_decision_copy(&decision->port, port) &&
        ch_decision_copy(&decision->network, network) && ch_decision_copy(&decision->source, context->source) &&
        ch_decision_copy(&decision->matcher_kind, selected == NULL ? "" : match.kind) &&
        ch_decision_copy(&decision->matcher_value, selected == NULL ? "" : match.value) &&
        ch_decision_copy(&decision->summary, summary);
    free(host); free(port); free(network);
    if (!copied) {
        ch_rule_decision_clear(decision);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule decision");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    decision->rule_number = number;
    decision->is_default = selected == NULL;
    uint64_t elapsed = uv_hrtime() - started;
    decision->elapsed_ns = elapsed > (uint64_t)INT64_MAX ? INT64_MAX : (long long)elapsed;
    return CH_OK;
}
