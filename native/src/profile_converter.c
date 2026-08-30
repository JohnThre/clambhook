// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "profile_converter.h"

#include <ctype.h>
#include <errno.h>
#include <openssl/evp.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <yaml.h>

#include "clambhook/json.h"
#include "internal.h"

#define CONVERTER_MAX_SOURCE (4U * 1024U * 1024U)
#define CONVERTER_MAX_ITEMS 4096U
#define CONVERTER_MAX_RULES 200000U
#define CONVERTER_MAX_WARNINGS 256U
#define CONVERTER_MAX_DEPTH 64U

typedef struct conv_strings {
    char **items;
    size_t count;
    size_t capacity;
} conv_strings;

typedef struct conv_proxy {
    char *name;
    char *type;
    char *host;
    int port;
    char *method;
    char *password;
    char *uuid;
    char *security;
    char *sni;
    char *alpn;
    char *dialer;
    char *shadow_password;
    char *private_key;
    char *public_key;
    char *preshared_key;
    char *address4;
    char *address6;
    bool tls;
    bool skip_verify;
    bool supported;
} conv_proxy;

typedef struct conv_group {
    char *name;
    char *type;
    conv_strings members;
    char *url;
    int64_t interval;
    int64_t timeout_ms;
    bool supported;
} conv_group;

typedef struct conv_document {
    conv_proxy *proxies;
    size_t proxy_count;
    size_t proxy_capacity;
    conv_group *groups;
    size_t group_count;
    size_t group_capacity;
    conv_strings rules;
    conv_strings warnings;
    char *format;
    char *profile_name;
    bool need_direct_chain;
} conv_document;

static char *conv_unquote(char *value);

static char *conv_trim_copy(const char *value) {
    if (value == NULL) return NULL;
    while (*value != '\0' && isspace((unsigned char)*value) != 0) ++value;
    const char *end = value + strlen(value);
    while (end > value && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - value);
    char *copy = malloc(length + 1U);
    if (copy != NULL) {
        memcpy(copy, value, length);
        copy[length] = '\0';
    }
    return copy;
}

static void conv_strings_clear(conv_strings *values) {
    if (values == NULL) return;
    for (size_t index = 0U; index < values->count; ++index) free(values->items[index]);
    free(values->items);
    memset(values, 0, sizeof(*values));
}

static ch_status conv_strings_add(conv_strings *values, const char *value,
                                  size_t limit, ch_error *error) {
    if (values->count >= limit) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "converter item limit exceeded");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (values->count == values->capacity) {
        size_t next = values->capacity == 0U ? 8U : values->capacity * 2U;
        char **grown = realloc(values->items, next * sizeof(*grown));
        if (grown == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "grow converter list");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        values->items = grown;
        values->capacity = next;
    }
    values->items[values->count] = ch_strdup(value == NULL ? "" : value);
    if (values->items[values->count] == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy converter value");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ++values->count;
    return CH_OK;
}

static void conv_warn(conv_document *document, const char *path,
                      const char *message) {
    if (document->warnings.count >= CONVERTER_MAX_WARNINGS) return;
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"code\":\"unsupported\",\"path\":") &&
        ch_json_append_string(&json, path == NULL ? "" : path) &&
        ch_json_append(&json, ",\"message\":") &&
        ch_json_append_string(&json, message == NULL ? "unsupported source feature" : message) &&
        ch_json_append(&json, "}");
    char *entry = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (entry != NULL) {
        ch_error ignored;
        (void)conv_strings_add(&document->warnings, entry,
                               CONVERTER_MAX_WARNINGS, &ignored);
        free(entry);
    }
}

static void conv_proxy_clear(conv_proxy *proxy) {
    if (proxy == NULL) return;
    free(proxy->name); free(proxy->type); free(proxy->host); free(proxy->method);
    free(proxy->password); free(proxy->uuid); free(proxy->security);
    free(proxy->sni); free(proxy->alpn); free(proxy->dialer);
    free(proxy->shadow_password);
    free(proxy->private_key); free(proxy->public_key);
    free(proxy->preshared_key); free(proxy->address4); free(proxy->address6);
    memset(proxy, 0, sizeof(*proxy));
}

static void conv_group_clear(conv_group *group) {
    if (group == NULL) return;
    free(group->name); free(group->type); free(group->url);
    conv_strings_clear(&group->members);
    memset(group, 0, sizeof(*group));
}

static void conv_document_clear(conv_document *document) {
    if (document == NULL) return;
    for (size_t index = 0U; index < document->proxy_count; ++index)
        conv_proxy_clear(&document->proxies[index]);
    for (size_t index = 0U; index < document->group_count; ++index)
        conv_group_clear(&document->groups[index]);
    free(document->proxies); free(document->groups);
    conv_strings_clear(&document->rules); conv_strings_clear(&document->warnings);
    free(document->format); free(document->profile_name);
    memset(document, 0, sizeof(*document));
}

static conv_proxy *conv_add_proxy(conv_document *document, ch_error *error) {
    if (document->proxy_count >= CONVERTER_MAX_ITEMS) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "too many source proxies");
        return NULL;
    }
    if (document->proxy_count == document->proxy_capacity) {
        size_t next = document->proxy_capacity == 0U ? 8U : document->proxy_capacity * 2U;
        conv_proxy *grown = realloc(document->proxies, next * sizeof(*grown));
        if (grown == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "grow source proxies");
            return NULL;
        }
        memset(grown + document->proxy_capacity, 0,
               (next - document->proxy_capacity) * sizeof(*grown));
        document->proxies = grown;
        document->proxy_capacity = next;
    }
    return &document->proxies[document->proxy_count++];
}

static conv_group *conv_add_group(conv_document *document, ch_error *error) {
    if (document->group_count >= CONVERTER_MAX_ITEMS) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "too many source groups");
        return NULL;
    }
    if (document->group_count == document->group_capacity) {
        size_t next = document->group_capacity == 0U ? 8U : document->group_capacity * 2U;
        conv_group *grown = realloc(document->groups, next * sizeof(*grown));
        if (grown == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "grow source groups");
            return NULL;
        }
        memset(grown + document->group_capacity, 0,
               (next - document->group_capacity) * sizeof(*grown));
        document->groups = grown;
        document->group_capacity = next;
    }
    return &document->groups[document->group_count++];
}

static const yaml_node_t *yaml_map_value_depth(yaml_document_t *document,
                                               const yaml_node_t *mapping,
                                               const char *key,
                                               size_t depth) {
    if (depth >= CONVERTER_MAX_DEPTH) return NULL;
    if (mapping == NULL || mapping->type != YAML_MAPPING_NODE) return NULL;
    for (yaml_node_pair_t *pair = mapping->data.mapping.pairs.start;
         pair < mapping->data.mapping.pairs.top; ++pair) {
        yaml_node_t *name = yaml_document_get_node(document, pair->key);
        yaml_node_t *value = yaml_document_get_node(document, pair->value);
        if (name != NULL && name->type == YAML_SCALAR_NODE &&
            strcmp((const char *)name->data.scalar.value, key) == 0) return value;
    }
    for (yaml_node_pair_t *pair = mapping->data.mapping.pairs.start;
         pair < mapping->data.mapping.pairs.top; ++pair) {
        yaml_node_t *name = yaml_document_get_node(document, pair->key);
        yaml_node_t *value = yaml_document_get_node(document, pair->value);
        if (name == NULL || name->type != YAML_SCALAR_NODE ||
            strcmp((const char *)name->data.scalar.value, "<<") != 0)
            continue;
        if (value != NULL && value->type == YAML_MAPPING_NODE) {
            const yaml_node_t *found = yaml_map_value_depth(
                document, value, key, depth + 1U);
            if (found != NULL) return found;
        } else if (value != NULL && value->type == YAML_SEQUENCE_NODE) {
            for (yaml_node_item_t *item = value->data.sequence.items.start;
                 item < value->data.sequence.items.top; ++item) {
                const yaml_node_t *found = yaml_map_value_depth(
                    document, yaml_document_get_node(document, *item), key,
                    depth + 1U);
                if (found != NULL) return found;
            }
        }
    }
    return NULL;
}

static const yaml_node_t *yaml_map_value(yaml_document_t *document,
                                         const yaml_node_t *mapping,
                                         const char *key) {
    return yaml_map_value_depth(document, mapping, key, 0U);
}

static const char *yaml_scalar(const yaml_node_t *node) {
    return node != NULL && node->type == YAML_SCALAR_NODE ?
        (const char *)node->data.scalar.value : NULL;
}

static ch_status conv_yaml_validate_node(yaml_document_t *document,
                                         const yaml_node_t *node,
                                         const yaml_node_t **stack,
                                         size_t depth, size_t *visits,
                                         ch_error *error) {
    if (node == NULL) return CH_OK;
    if (depth >= CONVERTER_MAX_DEPTH || ++*visits > CONVERTER_MAX_ITEMS * 8U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Mihomo YAML exceeds nesting or node limit");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    for (size_t index = 0U; index < depth; ++index) {
        if (stack[index] == node) {
            ch_error_set(error, CH_ERROR_PARSE, "Mihomo YAML contains an alias cycle");
            return CH_ERROR_PARSE;
        }
    }
    stack[depth] = node;
    if (node->type == YAML_SEQUENCE_NODE) {
        for (yaml_node_item_t *item = node->data.sequence.items.start;
             item < node->data.sequence.items.top; ++item) {
            ch_status status = conv_yaml_validate_node(
                document, yaml_document_get_node(document, *item), stack,
                depth + 1U, visits, error);
            if (status != CH_OK) return status;
        }
    } else if (node->type == YAML_MAPPING_NODE) {
        for (yaml_node_pair_t *outer = node->data.mapping.pairs.start;
             outer < node->data.mapping.pairs.top; ++outer) {
            const yaml_node_t *outer_key = yaml_document_get_node(document,
                                                                   outer->key);
            if (outer_key == NULL || outer_key->type != YAML_SCALAR_NODE) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "Mihomo YAML object keys must be strings");
                return CH_ERROR_PARSE;
            }
            for (yaml_node_pair_t *inner = outer + 1;
                 inner < node->data.mapping.pairs.top; ++inner) {
                const yaml_node_t *inner_key = yaml_document_get_node(
                    document, inner->key);
                if (inner_key != NULL && inner_key->type == YAML_SCALAR_NODE &&
                    strcmp((const char *)outer_key->data.scalar.value,
                           (const char *)inner_key->data.scalar.value) == 0) {
                    ch_error_set(error, CH_ERROR_PARSE,
                                 "Mihomo YAML contains a duplicate key");
                    return CH_ERROR_PARSE;
                }
            }
            ch_status status = conv_yaml_validate_node(
                document, yaml_document_get_node(document, outer->value),
                stack, depth + 1U, visits, error);
            if (status != CH_OK) return status;
        }
    }
    return CH_OK;
}

static char *yaml_string(yaml_document_t *document, const yaml_node_t *mapping,
                         const char *key) {
    const char *value = yaml_scalar(yaml_map_value(document, mapping, key));
    return ch_strdup(value == NULL ? "" : value);
}

static bool conv_parse_bool(const char *value, bool fallback) {
    if (value == NULL) return fallback;
    if (strcasecmp(value, "true") == 0 || strcmp(value, "1") == 0 ||
        strcasecmp(value, "yes") == 0) return true;
    if (strcasecmp(value, "false") == 0 || strcmp(value, "0") == 0 ||
        strcasecmp(value, "no") == 0) return false;
    return fallback;
}

static int64_t conv_parse_int(const char *value, int64_t fallback) {
    if (value == NULL || value[0] == '\0') return fallback;
    char *end = NULL;
    errno = 0;
    long long parsed = strtoll(value, &end, 10);
    return errno == 0 && end != value && *end == '\0' ? (int64_t)parsed : fallback;
}

static bool conv_cipher_supported(const char *method) {
    return method != NULL && (strcasecmp(method, "aes-128-gcm") == 0 ||
        strcasecmp(method, "aes-256-gcm") == 0 ||
        strcasecmp(method, "chacha20-ietf-poly1305") == 0);
}

static bool conv_name_exists(const conv_document *document, const char *name) {
    for (size_t index = 0U; index + 1U < document->proxy_count; ++index) {
        if (document->proxies[index].name != NULL && name != NULL &&
            strcmp(document->proxies[index].name, name) == 0) return true;
    }
    return false;
}

static ch_status conv_mihomo_proxy(conv_document *output,
                                   yaml_document_t *yaml,
                                   const yaml_node_t *node, size_t index,
                                   ch_error *error) {
    if (node == NULL || node->type != YAML_MAPPING_NODE) {
        conv_warn(output, "proxies", "non-object proxy entry was omitted");
        return CH_OK;
    }
    conv_proxy *proxy = conv_add_proxy(output, error);
    if (proxy == NULL) return error->code;
    proxy->name = yaml_string(yaml, node, "name");
    proxy->type = yaml_string(yaml, node, "type");
    proxy->host = yaml_string(yaml, node, "server");
    proxy->method = yaml_string(yaml, node, "cipher");
    proxy->password = yaml_string(yaml, node, "password");
    proxy->uuid = yaml_string(yaml, node, "uuid");
    proxy->security = yaml_string(yaml, node, "cipher");
    proxy->sni = yaml_string(yaml, node, "servername");
    if (proxy->sni != NULL && proxy->sni[0] == '\0') {
        free(proxy->sni); proxy->sni = yaml_string(yaml, node, "sni");
    }
    proxy->dialer = yaml_string(yaml, node, "dialer-proxy");
    proxy->private_key = yaml_string(yaml, node, "private-key");
    proxy->public_key = yaml_string(yaml, node, "public-key");
    proxy->preshared_key = yaml_string(yaml, node, "pre-shared-key");
    proxy->address4 = yaml_string(yaml, node, "ip");
    proxy->address6 = yaml_string(yaml, node, "ipv6");
    proxy->port = (int)conv_parse_int(yaml_scalar(yaml_map_value(yaml, node, "port")), 0);
    proxy->tls = conv_parse_bool(yaml_scalar(yaml_map_value(yaml, node, "tls")), false);
    proxy->skip_verify = conv_parse_bool(
        yaml_scalar(yaml_map_value(yaml, node, "skip-cert-verify")), false);
    char path[64];
    (void)snprintf(path, sizeof(path), "proxies[%zu]", index);
    if (proxy->name == NULL || proxy->type == NULL || proxy->host == NULL ||
        proxy->name[0] == '\0' || proxy->host[0] == '\0' || proxy->port < 1 ||
        proxy->port > 65535 || conv_name_exists(output, proxy->name)) {
        conv_warn(output, path, "proxy has a missing, invalid, or duplicate identity");
        return CH_OK;
    }
    const char *network = yaml_scalar(yaml_map_value(yaml, node, "network"));
    const yaml_node_t *reality = yaml_map_value(yaml, node, "reality-opts");
    if (network != NULL && network[0] != '\0' && strcasecmp(network, "tcp") != 0) {
        conv_warn(output, path, "non-TCP transport is not supported");
        return CH_OK;
    }
    if (reality != NULL) {
        conv_warn(output, path, "Reality transport is not supported");
        return CH_OK;
    }
    if (strcasecmp(proxy->type, "ss") == 0) {
        const char *plugin = yaml_scalar(yaml_map_value(yaml, node, "plugin"));
        if (!conv_cipher_supported(proxy->method) || proxy->password == NULL ||
            proxy->password[0] == '\0') {
            conv_warn(output, path, "Shadowsocks cipher is unsupported");
        } else if (plugin != NULL && plugin[0] != '\0' &&
                   strcasecmp(plugin, "shadow-tls") != 0) {
            conv_warn(output, path, "Shadowsocks plugin is unsupported");
        } else if (plugin != NULL && strcasecmp(plugin, "shadow-tls") == 0) {
            const yaml_node_t *options = yaml_map_value(yaml, node, "plugin-opts");
            const char *version = yaml_scalar(yaml_map_value(yaml, options, "version"));
            const char *password = yaml_scalar(yaml_map_value(yaml, options, "password"));
            const char *host = yaml_scalar(yaml_map_value(yaml, options, "host"));
            if (version == NULL || (strcmp(version, "3") != 0 && strcasecmp(version, "v3") != 0) ||
                password == NULL || password[0] == '\0') {
                conv_warn(output, path, "only complete ShadowTLS v3 settings are supported");
            } else {
                proxy->shadow_password = ch_strdup(password);
                if (host != NULL && host[0] != '\0') {
                    free(proxy->sni); proxy->sni = ch_strdup(host);
                }
                proxy->supported = proxy->shadow_password != NULL;
            }
        } else proxy->supported = true;
    } else if (strcasecmp(proxy->type, "vmess") == 0) {
        int64_t alter = conv_parse_int(yaml_scalar(yaml_map_value(yaml, node, "alterId")), 0);
        if (alter != 0 || proxy->uuid == NULL || proxy->uuid[0] == '\0' ||
            (proxy->security[0] != '\0' && strcasecmp(proxy->security, "auto") != 0 &&
             strcasecmp(proxy->security, "aes-128-gcm") != 0 &&
             strcasecmp(proxy->security, "chacha20-poly1305") != 0)) {
            conv_warn(output, path, "only VMess AEAD over raw TCP is supported");
        } else proxy->supported = true;
    } else if (strcasecmp(proxy->type, "trojan") == 0) {
        if (proxy->password == NULL || proxy->password[0] == '\0')
            conv_warn(output, path, "Trojan password is required");
        else proxy->supported = true;
    } else if (strcasecmp(proxy->type, "wireguard") == 0) {
        if (proxy->private_key == NULL || proxy->private_key[0] == '\0' ||
            proxy->public_key == NULL || proxy->public_key[0] == '\0' ||
            ((proxy->address4 == NULL || proxy->address4[0] == '\0') &&
             (proxy->address6 == NULL || proxy->address6[0] == '\0'))) {
            conv_warn(output, path, "WireGuard requires private/public keys and a tunnel address");
        } else {
            proxy->supported = true;
        }
    } else {
        conv_warn(output, path, "proxy protocol is unsupported");
    }
    return CH_OK;
}

static ch_status conv_mihomo_group(conv_document *output,
                                   yaml_document_t *yaml,
                                   const yaml_node_t *node, size_t index,
                                   ch_error *error) {
    if (node == NULL || node->type != YAML_MAPPING_NODE) return CH_OK;
    conv_group *group = conv_add_group(output, error);
    if (group == NULL) return error->code;
    group->name = yaml_string(yaml, node, "name");
    group->type = yaml_string(yaml, node, "type");
    group->url = yaml_string(yaml, node, "url");
    group->interval = conv_parse_int(yaml_scalar(yaml_map_value(yaml, node, "interval")), 300);
    group->timeout_ms = conv_parse_int(yaml_scalar(yaml_map_value(yaml, node, "timeout")), 5000);
    const yaml_node_t *members = yaml_map_value(yaml, node, "proxies");
    if (members != NULL && members->type == YAML_SEQUENCE_NODE) {
        for (yaml_node_item_t *item = members->data.sequence.items.start;
             item < members->data.sequence.items.top; ++item) {
            const char *member = yaml_scalar(yaml_document_get_node(yaml, *item));
            if (member != NULL) {
                if (strcasecmp(member, "DIRECT") == 0) output->need_direct_chain = true;
                if (conv_strings_add(&group->members, member, CONVERTER_MAX_ITEMS, error) != CH_OK)
                    return error->code;
            }
        }
    }
    char path[64];
    (void)snprintf(path, sizeof(path), "proxy-groups[%zu]", index);
    if (strcasecmp(group->type, "select") == 0) group->supported = true;
    else if (strcasecmp(group->type, "url-test") == 0) group->supported = true;
    else if (strcasecmp(group->type, "fallback") == 0) {
        group->supported = true;
        conv_warn(output, path, "fallback group was converted to url-test");
    } else conv_warn(output, path, "policy-group type is unsupported");
    return CH_OK;
}

static ch_status conv_mihomo_rule(conv_document *output,
                                  yaml_document_t *yaml,
                                  const yaml_node_t *providers,
                                  const char *rule, size_t index,
                                  ch_error *error) {
    if (strncasecmp(rule, "RULE-SET,", 9U) != 0) {
        return conv_strings_add(&output->rules, rule, CONVERTER_MAX_RULES,
                                error);
    }
    char *copy = ch_strdup(rule);
    if (copy == NULL) return CH_ERROR_OUT_OF_MEMORY;
    char *save = NULL;
    (void)strtok_r(copy, ",", &save);
    char *provider_name = conv_unquote(strtok_r(NULL, ",", &save));
    char *policy = conv_unquote(strtok_r(NULL, ",", &save));
    const yaml_node_t *provider = yaml_map_value(yaml, providers,
                                                 provider_name == NULL ? "" :
                                                                         provider_name);
    const yaml_node_t *payload = yaml_map_value(yaml, provider, "payload");
    const char *behavior = yaml_scalar(yaml_map_value(yaml, provider,
                                                       "behavior"));
    char path[96];
    (void)snprintf(path, sizeof(path), "rules[%zu]", index);
    if (policy == NULL || payload == NULL ||
        payload->type != YAML_SEQUENCE_NODE || behavior == NULL) {
        conv_warn(output, path,
                  "remote or incomplete rule provider was left disabled for manual review");
        free(copy);
        return CH_OK;
    }
    ch_status status = CH_OK;
    for (yaml_node_item_t *item = payload->data.sequence.items.start;
         item < payload->data.sequence.items.top && status == CH_OK; ++item) {
        const char *entry = yaml_scalar(yaml_document_get_node(yaml, *item));
        if (entry == NULL || entry[0] == '\0') continue;
        ch_json_buffer expanded;
        ch_json_init(&expanded);
        int okay;
        if (strcasecmp(behavior, "classical") == 0) {
            okay = ch_json_append(&expanded, entry) &&
                ch_json_append(&expanded, ",") &&
                ch_json_append(&expanded, policy);
        } else if (strcasecmp(behavior, "domain") == 0) {
            const char *value = entry;
            const char *kind = "DOMAIN";
            if (strncmp(value, "+.", 2U) == 0) {
                value += 2U; kind = "DOMAIN-SUFFIX";
            } else if (value[0] == '.') {
                ++value; kind = "DOMAIN-SUFFIX";
            }
            okay = ch_json_append(&expanded, kind) &&
                ch_json_append(&expanded, ",") &&
                ch_json_append(&expanded, value) &&
                ch_json_append(&expanded, ",") &&
                ch_json_append(&expanded, policy);
        } else if (strcasecmp(behavior, "ipcidr") == 0) {
            okay = ch_json_append(&expanded,
                                  strchr(entry, ':') == NULL ? "IP-CIDR," :
                                                               "IP-CIDR6,") &&
                ch_json_append(&expanded, entry) &&
                ch_json_append(&expanded, ",") &&
                ch_json_append(&expanded, policy);
        } else {
            okay = 0;
        }
        char *expanded_rule = okay ? ch_json_take(&expanded) : NULL;
        ch_json_dispose(&expanded);
        if (expanded_rule == NULL) {
            conv_warn(output, path, "rule-provider behavior is unsupported");
            break;
        }
        status = conv_strings_add(&output->rules, expanded_rule,
                                  CONVERTER_MAX_RULES, error);
        free(expanded_rule);
    }
    free(copy);
    return status;
}

static ch_status conv_parse_mihomo(const char *source, conv_document *output,
                                   ch_error *error) {
    yaml_parser_t parser;
    yaml_document_t yaml;
    memset(&yaml, 0, sizeof(yaml));
    if (yaml_parser_initialize(&parser) == 0) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "initialize YAML parser");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    yaml_parser_set_input_string(&parser, (const unsigned char *)source, strlen(source));
    if (yaml_parser_load(&parser, &yaml) == 0) {
        yaml_parser_delete(&parser);
        ch_error_set(error, CH_ERROR_PARSE, "Mihomo YAML is malformed");
        return CH_ERROR_PARSE;
    }
    yaml_parser_delete(&parser);
    yaml_node_t *root = yaml_document_get_root_node(&yaml);
    if (root == NULL || root->type != YAML_MAPPING_NODE) {
        yaml_document_delete(&yaml);
        ch_error_set(error, CH_ERROR_PARSE, "Mihomo YAML root must be an object");
        return CH_ERROR_PARSE;
    }
    const yaml_node_t *stack[CONVERTER_MAX_DEPTH];
    size_t visits = 0U;
    ch_status validation = conv_yaml_validate_node(
        &yaml, root, stack, 0U, &visits, error);
    if (validation != CH_OK) {
        yaml_document_delete(&yaml);
        return validation;
    }
    const yaml_node_t *proxies = yaml_map_value(&yaml, root, "proxies");
    if (proxies != NULL && proxies->type == YAML_SEQUENCE_NODE) {
        size_t index = 0U;
        for (yaml_node_item_t *item = proxies->data.sequence.items.start;
             item < proxies->data.sequence.items.top; ++item, ++index) {
            ch_status status = conv_mihomo_proxy(output, &yaml,
                yaml_document_get_node(&yaml, *item), index, error);
            if (status != CH_OK) { yaml_document_delete(&yaml); return status; }
        }
    }
    const yaml_node_t *groups = yaml_map_value(&yaml, root, "proxy-groups");
    if (groups != NULL && groups->type == YAML_SEQUENCE_NODE) {
        size_t index = 0U;
        for (yaml_node_item_t *item = groups->data.sequence.items.start;
             item < groups->data.sequence.items.top; ++item, ++index) {
            ch_status status = conv_mihomo_group(output, &yaml,
                yaml_document_get_node(&yaml, *item), index, error);
            if (status != CH_OK) { yaml_document_delete(&yaml); return status; }
        }
    }
    const yaml_node_t *providers = yaml_map_value(&yaml, root, "rule-providers");
    const yaml_node_t *rules = yaml_map_value(&yaml, root, "rules");
    if (rules != NULL && rules->type == YAML_SEQUENCE_NODE) {
        size_t index = 0U;
        for (yaml_node_item_t *item = rules->data.sequence.items.start;
             item < rules->data.sequence.items.top; ++item, ++index) {
            const char *rule = yaml_scalar(yaml_document_get_node(&yaml, *item));
            if (rule != NULL && conv_mihomo_rule(output, &yaml, providers,
                                                 rule, index, error) != CH_OK) {
                yaml_document_delete(&yaml); return error->code;
            }
        }
    }
    if (yaml_map_value(&yaml, root, "proxy-providers") != NULL)
        conv_warn(output, "proxy-providers", "remote proxy providers are not fetched during offline conversion");
    if (providers != NULL)
        conv_warn(output, "rule-providers",
                  "inline compatible payloads were expanded; remote provider URLs were not fetched");
    if (yaml_map_value(&yaml, root, "dns") != NULL)
        conv_warn(output, "dns", "source DNS policy requires manual review and was omitted");
    if (yaml_map_value(&yaml, root, "tun") != NULL)
        conv_warn(output, "tun", "source TUN policy requires manual review and was omitted");
    yaml_document_delete(&yaml);
    return CH_OK;
}

static char *conv_unquote(char *value) {
    while (*value != '\0' && isspace((unsigned char)*value) != 0) ++value;
    size_t length = strlen(value);
    while (length > 0U && isspace((unsigned char)value[length - 1U]) != 0)
        value[--length] = '\0';
    if (length >= 2U && ((value[0] == '"' && value[length - 1U] == '"') ||
                        (value[0] == '\'' && value[length - 1U] == '\''))) {
        value[length - 1U] = '\0'; ++value;
    }
    return value;
}

static ch_status conv_parse_surge_proxy(conv_document *output, char *name,
                                        char *value, size_t line,
                                        ch_error *error) {
    conv_proxy *proxy = conv_add_proxy(output, error);
    if (proxy == NULL) return error->code;
    proxy->name = ch_strdup(conv_unquote(name));
    char *save = NULL;
    char *type = strtok_r(value, ",", &save);
    char *host = strtok_r(NULL, ",", &save);
    char *port = strtok_r(NULL, ",", &save);
    proxy->type = ch_strdup(conv_unquote(type == NULL ? "" : type));
    proxy->host = ch_strdup(conv_unquote(host == NULL ? "" : host));
    proxy->port = (int)conv_parse_int(conv_unquote(port == NULL ? "" : port), 0);
    for (char *token = strtok_r(NULL, ",", &save); token != NULL;
         token = strtok_r(NULL, ",", &save)) {
        char *item = conv_unquote(token);
        char *equal = strchr(item, '=');
        if (equal == NULL) continue;
        *equal++ = '\0';
        char *key = conv_unquote(item);
        char *setting = conv_unquote(equal);
        if (strcasecmp(key, "password") == 0) proxy->password = ch_strdup(setting);
        else if (strcasecmp(key, "encrypt-method") == 0) proxy->method = ch_strdup(setting);
        else if (strcasecmp(key, "username") == 0) proxy->uuid = ch_strdup(setting);
        else if (strcasecmp(key, "sni") == 0) proxy->sni = ch_strdup(setting);
        else if (strcasecmp(key, "underlying-proxy") == 0) proxy->dialer = ch_strdup(setting);
        else if (strcasecmp(key, "skip-cert-verify") == 0) proxy->skip_verify = conv_parse_bool(setting, false);
        else if (strcasecmp(key, "tls") == 0) proxy->tls = conv_parse_bool(setting, false);
        else if (strcasecmp(key, "vmess-aead") == 0 && !conv_parse_bool(setting, false)) proxy->security = ch_strdup("legacy");
        else if (strcasecmp(key, "encrypt-method") == 0) proxy->security = ch_strdup(setting);
        else if (strcasecmp(key, "ws") == 0 && conv_parse_bool(setting, false)) proxy->alpn = ch_strdup("unsupported-ws");
    }
    char path[64]; (void)snprintf(path, sizeof(path), "Proxy line %zu", line);
    if (proxy->name == NULL || proxy->name[0] == '\0' || proxy->host == NULL ||
        proxy->host[0] == '\0' || proxy->port < 1 || proxy->port > 65535 ||
        conv_name_exists(output, proxy->name)) {
        conv_warn(output, path, "proxy has a missing, invalid, or duplicate identity");
    } else if (strcasecmp(proxy->type, "ss") == 0 && conv_cipher_supported(proxy->method) &&
               proxy->password != NULL && proxy->password[0] != '\0') proxy->supported = true;
    else if (strcasecmp(proxy->type, "vmess") == 0 && proxy->uuid != NULL &&
             proxy->uuid[0] != '\0' && proxy->security == NULL && proxy->alpn == NULL)
        proxy->supported = true;
    else if (strcasecmp(proxy->type, "trojan") == 0 && proxy->password != NULL &&
             proxy->password[0] != '\0' && proxy->alpn == NULL) proxy->supported = true;
    else conv_warn(output, path, "proxy protocol, cipher, or transport is unsupported");
    return CH_OK;
}

static ch_status conv_parse_surge_group(conv_document *output, char *name,
                                        char *value, size_t line,
                                        ch_error *error) {
    conv_group *group = conv_add_group(output, error);
    if (group == NULL) return error->code;
    group->name = ch_strdup(conv_unquote(name));
    char *save = NULL;
    char *type = strtok_r(value, ",", &save);
    group->type = ch_strdup(conv_unquote(type == NULL ? "" : type));
    group->interval = 300; group->timeout_ms = 5000;
    for (char *token = strtok_r(NULL, ",", &save); token != NULL;
         token = strtok_r(NULL, ",", &save)) {
        char *item = conv_unquote(token);
        char *equal = strchr(item, '=');
        if (equal != NULL) {
            *equal++ = '\0';
            if (strcasecmp(conv_unquote(item), "url") == 0) group->url = ch_strdup(conv_unquote(equal));
            continue;
        }
        if (strcasecmp(item, "DIRECT") == 0) output->need_direct_chain = true;
        if (conv_strings_add(&group->members, item, CONVERTER_MAX_ITEMS, error) != CH_OK)
            return error->code;
    }
    char path[64]; (void)snprintf(path, sizeof(path), "Proxy Group line %zu", line);
    if (strcasecmp(group->type, "select") == 0 ||
        strcasecmp(group->type, "url-test") == 0) group->supported = true;
    else if (strcasecmp(group->type, "smart") == 0 ||
             strcasecmp(group->type, "fallback") == 0) {
        group->supported = true;
        conv_warn(output, path, "group was converted to url-test");
    } else conv_warn(output, path, "policy-group type is unsupported");
    return CH_OK;
}

static ch_status conv_parse_surge(const char *source, conv_document *output,
                                  ch_error *error) {
    char *copy = ch_strdup(source);
    if (copy == NULL) { ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy Surge profile"); return CH_ERROR_OUT_OF_MEMORY; }
    enum { SURGE_NONE, SURGE_PROXY, SURGE_GROUP, SURGE_RULE } section = SURGE_NONE;
    char *save = NULL; size_t line_number = 0U;
    for (char *line = strtok_r(copy, "\n", &save); line != NULL;
         line = strtok_r(NULL, "\n", &save)) {
        ++line_number;
        char *trimmed = conv_unquote(line);
        if (trimmed[0] == '\0' || trimmed[0] == '#' || trimmed[0] == ';') continue;
        if (trimmed[0] == '[') {
            section = strcasecmp(trimmed, "[Proxy]") == 0 ? SURGE_PROXY :
                (strcasecmp(trimmed, "[Proxy Group]") == 0 ? SURGE_GROUP :
                 (strcasecmp(trimmed, "[Rule]") == 0 ? SURGE_RULE : SURGE_NONE));
            continue;
        }
        if (section == SURGE_RULE) {
            if (conv_strings_add(&output->rules, trimmed, CONVERTER_MAX_RULES, error) != CH_OK) {
                free(copy); return error->code;
            }
            continue;
        }
        if (section != SURGE_PROXY && section != SURGE_GROUP) continue;
        char *equal = strchr(trimmed, '=');
        if (equal == NULL) continue;
        *equal++ = '\0';
        ch_status status = section == SURGE_PROXY ?
            conv_parse_surge_proxy(output, trimmed, equal, line_number, error) :
            conv_parse_surge_group(output, trimmed, equal, line_number, error);
        if (status != CH_OK) { free(copy); return status; }
    }
    free(copy);
    if (strstr(source, "[WireGuard ") != NULL)
        conv_warn(output, "WireGuard", "WireGuard sections require manual review and were omitted");
    if (strstr(source, "[MITM]") != NULL || strstr(source, "[Script]") != NULL ||
        strstr(source, "[URL Rewrite]") != NULL)
        conv_warn(output, "developer", "MITM, scripts, and rewrites are not imported");
    return CH_OK;
}

static const conv_proxy *conv_proxy_named(const conv_document *document,
                                          const char *name) {
    for (size_t index = 0U; index < document->proxy_count; ++index)
        if (document->proxies[index].supported && document->proxies[index].name != NULL &&
            name != NULL && strcmp(document->proxies[index].name, name) == 0)
            return &document->proxies[index];
    return NULL;
}

static const conv_group *conv_group_named(const conv_document *document,
                                          const char *name) {
    for (size_t index = 0U; index < document->group_count; ++index)
        if (document->groups[index].supported && document->groups[index].name != NULL &&
            name != NULL && strcmp(document->groups[index].name, name) == 0)
            return &document->groups[index];
    return NULL;
}

static ch_status conv_group_leaves(const conv_document *document,
                                   const conv_group *group,
                                   conv_strings *leaves,
                                   const conv_group **stack, size_t depth,
                                   ch_error *error) {
    if (depth >= CONVERTER_MAX_DEPTH) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy-group nesting exceeds converter limit");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    for (size_t index = 0U; index < depth; ++index) {
        if (stack[index] == group) return CH_ERROR_INVALID_STATE;
    }
    stack[depth] = group;
    for (size_t index = 0U; index < group->members.count; ++index) {
        const char *name = group->members.items[index];
        if (strcasecmp(name, "DIRECT") == 0 ||
            conv_proxy_named(document, name) != NULL) {
            bool duplicate = false;
            for (size_t existing = 0U; existing < leaves->count; ++existing)
                if (strcmp(leaves->items[existing], name) == 0) duplicate = true;
            if (!duplicate && conv_strings_add(leaves, name,
                                                CONVERTER_MAX_ITEMS,
                                                error) != CH_OK)
                return error->code;
            continue;
        }
        const conv_group *nested = conv_group_named(document, name);
        if (nested != NULL) {
            ch_status status = conv_group_leaves(document, nested, leaves,
                                                  stack, depth + 1U, error);
            if (status != CH_OK) return status;
        }
    }
    return CH_OK;
}

static int conv_toml_string(ch_json_buffer *toml, const char *value) {
    return ch_json_append_string(toml, value == NULL ? "" : value);
}

static int conv_emit_server(ch_json_buffer *toml, const conv_proxy *proxy) {
    char address[1024];
    bool ipv6 = proxy->host != NULL && strchr(proxy->host, ':') != NULL && proxy->host[0] != '[';
    (void)snprintf(address, sizeof(address), ipv6 ? "[%s]:%d" : "%s:%d",
                   proxy->host == NULL ? "" : proxy->host, proxy->port);
    const char *protocol = strcasecmp(proxy->type, "ss") == 0 ? "shadowsocks" : proxy->type;
    int okay = ch_json_append(toml, "[[profile.chain.server]]\nname = ") &&
        conv_toml_string(toml, proxy->name) && ch_json_append(toml, "\naddress = ") &&
        conv_toml_string(toml, address) && ch_json_append(toml, "\nprotocol = ") &&
        conv_toml_string(toml, protocol) && ch_json_append(toml, "\n[profile.chain.server.settings]\n");
    if (!okay) return 0;
    if (strcasecmp(proxy->type, "ss") == 0) {
        okay = ch_json_append(toml, "method = ") && conv_toml_string(toml, proxy->method) &&
            ch_json_append(toml, "\npassword = ") && conv_toml_string(toml, proxy->password);
    } else if (strcasecmp(proxy->type, "vmess") == 0) {
        okay = ch_json_append(toml, "uuid = ") && conv_toml_string(toml, proxy->uuid) &&
            ch_json_append(toml, "\nsecurity = ") &&
            conv_toml_string(toml, proxy->security == NULL || proxy->security[0] == '\0' ? "auto" : proxy->security) &&
            ch_json_append_format(toml, "\ntls = %s", proxy->tls ? "true" : "false");
    } else if (strcasecmp(proxy->type, "wireguard") == 0) {
        okay = ch_json_append(toml, "private_key = ") &&
            conv_toml_string(toml, proxy->private_key) &&
            ch_json_append(toml, "\naddresses = [");
        bool has_address = false;
        if (okay && proxy->address4 != NULL && proxy->address4[0] != '\0') {
            okay = conv_toml_string(toml, proxy->address4);
            has_address = true;
        }
        if (okay && proxy->address6 != NULL && proxy->address6[0] != '\0') {
            if (has_address) okay = ch_json_append(toml, ", ");
            if (okay) okay = conv_toml_string(toml, proxy->address6);
        }
        if (okay) okay = ch_json_append(toml,
            "]\n[[profile.chain.server.settings.peers]]\npublic_key = ") &&
            conv_toml_string(toml, proxy->public_key) &&
            ch_json_append(toml, "\nendpoint = ") &&
            conv_toml_string(toml, address) &&
            ch_json_append(toml,
                "\nallowed_ips = [\"0.0.0.0/0\", \"::/0\"]");
        if (okay && proxy->preshared_key != NULL && proxy->preshared_key[0] != '\0')
            okay = ch_json_append(toml, "\npreshared_key = ") &&
                conv_toml_string(toml, proxy->preshared_key);
    } else {
        okay = ch_json_append(toml, "password = ") && conv_toml_string(toml, proxy->password);
    }
    if (okay && proxy->sni != NULL && proxy->sni[0] != '\0')
        okay = ch_json_append(toml, "\nsni = ") && conv_toml_string(toml, proxy->sni);
    if (okay && proxy->skip_verify)
        okay = ch_json_append(toml, "\nskip_cert_verify = true");
    return okay && ch_json_append(toml, "\n\n");
}

static int conv_emit_proxy_chain(ch_json_buffer *toml,
                                 const conv_document *document,
                                 const conv_proxy *proxy) {
    int okay = ch_json_append(toml, "[[profile.chain]]\nname = ") &&
        conv_toml_string(toml, proxy->name) && ch_json_append(toml, "\n\n");
    if (!okay) return 0;
    const conv_proxy *dialers[CONVERTER_MAX_DEPTH];
    size_t dialer_count = 0U;
    const conv_proxy *cursor = proxy;
    while (cursor->dialer != NULL && cursor->dialer[0] != '\0' &&
           dialer_count < CONVERTER_MAX_DEPTH) {
        cursor = conv_proxy_named(document, cursor->dialer);
        if (cursor == NULL) break;
        dialers[dialer_count++] = cursor;
    }
    while (okay && dialer_count > 0U)
        okay = conv_emit_server(toml, dialers[--dialer_count]);
    if (okay && proxy->shadow_password != NULL) {
        conv_proxy shadow = *proxy;
        shadow.name = "shadowtls";
        shadow.type = "shadowtls";
        shadow.password = proxy->shadow_password;
        okay = conv_emit_server(toml, &shadow);
    }
    return okay && conv_emit_server(toml, proxy);
}

static bool conv_proxy_chain_valid(const conv_document *document,
                                   const conv_proxy *proxy,
                                   const conv_proxy **stack, size_t depth) {
    if (depth >= CONVERTER_MAX_DEPTH) return false;
    for (size_t index = 0U; index < depth; ++index)
        if (stack[index] == proxy) return false;
    if (proxy->dialer == NULL || proxy->dialer[0] == '\0') return true;
    stack[depth] = proxy;
    const conv_proxy *dialer = conv_proxy_named(document, proxy->dialer);
    return dialer != NULL && conv_proxy_chain_valid(
        document, dialer, stack, depth + 1U);
}

static char *conv_rule_action(const conv_document *document, const char *source) {
    if (source == NULL) return NULL;
    if (strcasecmp(source, "DIRECT") == 0) return ch_strdup("direct");
    if (strcasecmp(source, "REJECT") == 0 || strcasecmp(source, "REJECT-DROP") == 0)
        return ch_strdup("block");
    if (conv_proxy_named(document, source) != NULL) {
        size_t length = strlen(source) + 7U; char *result = malloc(length);
        if (result != NULL) (void)snprintf(result, length, "chain:%s", source);
        return result;
    }
    if (conv_group_named(document, source) != NULL) {
        size_t length = strlen(source) + 7U; char *result = malloc(length);
        if (result != NULL) (void)snprintf(result, length, "group:%s", source);
        return result;
    }
    return NULL;
}

static int conv_emit_rule(ch_json_buffer *toml, conv_document *document,
                          const char *raw, size_t index) {
    char *copy = ch_strdup(raw);
    if (copy == NULL) return 0;
    char *save = NULL;
    char *kind = conv_unquote(strtok_r(copy, ",", &save));
    char *value = NULL; char *policy = NULL;
    bool final = kind != NULL && (strcasecmp(kind, "MATCH") == 0 || strcasecmp(kind, "FINAL") == 0);
    if (final) policy = conv_unquote(strtok_r(NULL, ",", &save));
    else {
        value = conv_unquote(strtok_r(NULL, ",", &save));
        policy = conv_unquote(strtok_r(NULL, ",", &save));
    }
    char *action = conv_rule_action(document, policy);
    if (kind == NULL || action == NULL || (!final && (value == NULL || value[0] == '\0'))) {
        char path[64]; (void)snprintf(path, sizeof(path), "rules[%zu]", index);
        conv_warn(document, path, "rule target or matcher is unsupported");
        free(action); free(copy); return 1;
    }
    const char *field = NULL;
    if (final) field = NULL;
    else if (strcasecmp(kind, "DOMAIN") == 0) field = "domains";
    else if (strcasecmp(kind, "DOMAIN-SUFFIX") == 0) field = "domain_suffixes";
    else if (strcasecmp(kind, "DOMAIN-KEYWORD") == 0) field = "domain_keywords";
    else if (strcasecmp(kind, "IP-CIDR") == 0 || strcasecmp(kind, "IP-CIDR6") == 0) field = "cidrs";
    else if (strcasecmp(kind, "SRC-IP-CIDR") == 0) field = "source_cidrs";
    else if (strcasecmp(kind, "PROCESS-NAME") == 0) field = "processes";
    else if (strcasecmp(kind, "NETWORK") == 0) field = "networks";
    else if (strcasecmp(kind, "DST-PORT") == 0 && strchr(value, '-') == NULL) field = "ports";
    if (!final && field == NULL) {
        char path[64]; (void)snprintf(path, sizeof(path), "rules[%zu]", index);
        conv_warn(document, path, "rule matcher is unsupported");
        free(action); free(copy); return 1;
    }
    int okay = ch_json_append(toml, "[[profile.rule]]\nname = ") &&
        conv_toml_string(toml, raw) && ch_json_append(toml, "\naction = ") &&
        conv_toml_string(toml, action);
    if (okay && field != NULL) {
        if (strcmp(field, "ports") == 0)
            okay = ch_json_append_format(toml, "\nports = [%lld]",
                                         (long long)conv_parse_int(value, -1));
        else okay = ch_json_append(toml, "\n") && ch_json_append(toml, field) &&
            ch_json_append(toml, " = [") && conv_toml_string(toml, value) &&
            ch_json_append(toml, "]");
    }
    okay = okay && ch_json_append(toml, "\n\n");
    free(action); free(copy); return okay;
}

static ch_status conv_render(conv_document *document, char **out_toml,
                             ch_error *error) {
    for (size_t index = 0U; index < document->proxy_count; ++index) {
        conv_proxy *proxy = &document->proxies[index];
        if (!proxy->supported || proxy->dialer == NULL ||
            proxy->dialer[0] == '\0') continue;
        const conv_proxy *stack[CONVERTER_MAX_DEPTH];
        if (!conv_proxy_chain_valid(document, proxy, stack, 0U)) {
            char path[128];
            (void)snprintf(path, sizeof(path), "proxy %s",
                           proxy->name == NULL ? "" : proxy->name);
            conv_warn(document, path,
                      "carrier chain is missing, unsupported, cyclic, or too deep");
            proxy->supported = false;
        }
    }
    size_t usable = 0U;
    for (size_t index = 0U; index < document->proxy_count; ++index)
        if (document->proxies[index].supported) ++usable;
    if (usable == 0U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "source contains no usable ClambHook proxy chains");
        return CH_ERROR_UNSUPPORTED;
    }
    ch_json_buffer toml; ch_json_init(&toml);
    int okay = ch_json_append(&toml, "active = ") &&
        conv_toml_string(&toml, document->profile_name) &&
        ch_json_append(&toml, "\n\n[[profile]]\nname = ") &&
        conv_toml_string(&toml, document->profile_name) && ch_json_append(&toml, "\n\n");
    if (document->need_direct_chain && okay) {
        okay = ch_json_append(&toml, "[[profile.chain]]\nname = \"DIRECT\"\n") &&
            ch_json_append(&toml, "[[profile.chain.server]]\nname = \"DIRECT\"\nprotocol = \"direct\"\n\n");
    }
    for (size_t index = 0U; okay && index < document->proxy_count; ++index)
        if (document->proxies[index].supported)
            okay = conv_emit_proxy_chain(&toml, document, &document->proxies[index]);
    for (size_t index = 0U; okay && index < document->group_count; ++index) {
        conv_group *group = &document->groups[index];
        if (!group->supported) continue;
        conv_strings leaves = {0};
        const conv_group *stack[CONVERTER_MAX_DEPTH];
        ch_status leaf_status = conv_group_leaves(
            document, group, &leaves, stack, 0U, error);
        if (leaf_status == CH_ERROR_INVALID_STATE) {
            char path[128];
            (void)snprintf(path, sizeof(path), "policy-group %s",
                           group->name == NULL ? "" : group->name);
            conv_warn(document, path, "cyclic policy group was omitted");
            group->supported = false;
            conv_strings_clear(&leaves);
            continue;
        }
        if (leaf_status != CH_OK) {
            conv_strings_clear(&leaves);
            ch_json_dispose(&toml);
            return leaf_status;
        }
        ch_json_buffer members; ch_json_init(&members);
        size_t member_count = 0U;
        for (size_t item = 0U; item < leaves.count; ++item) {
            const char *name = leaves.items[item];
            if (member_count > 0U) (void)ch_json_append(&members, ", ");
            (void)conv_toml_string(&members, name); ++member_count;
        }
        if (member_count == 0U) {
            char path[128]; (void)snprintf(path, sizeof(path), "policy-group %s", group->name == NULL ? "" : group->name);
            conv_warn(document, path, "group has no usable leaf chains");
            group->supported = false;
            ch_json_dispose(&members); conv_strings_clear(&leaves); continue;
        }
        const char *type = strcasecmp(group->type, "select") == 0 ? "select" : "url-test";
        okay = ch_json_append(&toml, "[[profile.policy_group]]\nname = ") &&
            conv_toml_string(&toml, group->name) && ch_json_append(&toml, "\ntype = ") &&
            conv_toml_string(&toml, type) && ch_json_append(&toml, "\nchains = [") &&
            ch_json_append(&toml, members.data == NULL ? "" : members.data) &&
            ch_json_append(&toml, "]");
        if (okay && strcmp(type, "select") == 0)
            okay = ch_json_append(&toml, "\nselected = ") &&
                conv_toml_string(&toml, leaves.items[0]);
        if (okay && strcmp(type, "url-test") == 0) {
            okay = ch_json_append(&toml, "\ntest_url = ") && conv_toml_string(&toml,
                group->url == NULL || group->url[0] == '\0' ? "https://www.gstatic.com/generate_204" : group->url) &&
                ch_json_append_format(&toml, "\ninterval = \"%llds\"\ntimeout = \"%lldms\"",
                    (long long)(group->interval > 0 ? group->interval : 300),
                    (long long)(group->timeout_ms > 0 ? group->timeout_ms : 5000));
        }
        okay = okay && ch_json_append(&toml, "\n\n");
        ch_json_dispose(&members);
        conv_strings_clear(&leaves);
    }
    for (size_t index = 0U; okay && index < document->rules.count; ++index)
        okay = conv_emit_rule(&toml, document, document->rules.items[index], index);
    if (!okay || toml.length > CONVERTER_MAX_SOURCE) {
        ch_json_dispose(&toml);
        ch_error_set(error, okay ? CH_ERROR_INVALID_ARGUMENT : CH_ERROR_OUT_OF_MEMORY,
                     okay ? "converted TOML exceeds size limit" : "render converted TOML");
        return okay ? CH_ERROR_INVALID_ARGUMENT : CH_ERROR_OUT_OF_MEMORY;
    }
    *out_toml = ch_json_take(&toml); ch_json_dispose(&toml);
    ch_config *validated = NULL;
    ch_status status = ch_config_parse(*out_toml, NULL, &validated, error);
    ch_config_free(validated);
    if (status != CH_OK) { free(*out_toml); *out_toml = NULL; }
    return status;
}

static ch_status conv_sha256(const char *value, char output[65], ch_error *error) {
    EVP_MD_CTX *context = EVP_MD_CTX_new(); unsigned char digest[32]; unsigned int length = 0U;
    bool okay = context != NULL && EVP_DigestInit_ex(context, EVP_sha256(), NULL) == 1 &&
        EVP_DigestUpdate(context, value, strlen(value)) == 1 &&
        EVP_DigestFinal_ex(context, digest, &length) == 1 && length == sizeof(digest);
    EVP_MD_CTX_free(context);
    if (!okay) { ch_error_set(error, CH_ERROR_INTERNAL, "hash converted profile"); return CH_ERROR_INTERNAL; }
    for (size_t index = 0U; index < sizeof(digest); ++index)
        (void)snprintf(output + index * 2U, 3U, "%02x", digest[index]);
    output[64] = '\0'; return CH_OK;
}

static ch_status conv_request(const char *request_json, conv_document *document,
                              char **out_toml, char sha[65], ch_error *error) {
    if (request_json == NULL || strlen(request_json) > CONVERTER_MAX_SOURCE) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "converter request exceeds size limit");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_json_value *request = ch_json_parse(request_json, strlen(request_json), error);
    if (request == NULL) return error->code;
    const char *source = ch_json_string_value(ch_json_object_get(request, "source"));
    const char *format = ch_json_string_value(ch_json_object_get(request, "format"));
    const char *name = ch_json_string_value(ch_json_object_get(request, "profile_name"));
    if (source == NULL) {
        const ch_json_value *documents = ch_json_object_get(request, "documents");
        const ch_json_value *first = ch_json_array_get(documents, 0U);
        source = ch_json_string_value(ch_json_object_get(first, "content"));
        if (format == NULL) format = ch_json_string_value(ch_json_object_get(first, "format"));
        if (name == NULL) name = ch_json_string_value(ch_json_object_get(first, "profile_name"));
    }
    char *trimmed_name = conv_trim_copy(name == NULL ? "Imported Profile" : name);
    if (source == NULL || source[0] == '\0' || strlen(source) > CONVERTER_MAX_SOURCE ||
        trimmed_name == NULL || trimmed_name[0] == '\0') {
        free(trimmed_name); ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "source and profile_name are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *detected = format;
    if (detected == NULL || detected[0] == '\0' || strcasecmp(detected, "auto") == 0) {
        const char *cursor = source; while (*cursor != '\0' && isspace((unsigned char)*cursor) != 0) ++cursor;
        detected = *cursor == '[' ? "surge" : "mihomo";
    }
    document->format = ch_strdup(detected); document->profile_name = trimmed_name;
    ch_status status;
    if (document->format == NULL) status = CH_ERROR_OUT_OF_MEMORY;
    else if (strcasecmp(detected, "mihomo") == 0 || strcasecmp(detected, "yaml") == 0)
        status = conv_parse_mihomo(source, document, error);
    else if (strcasecmp(detected, "surge") == 0)
        status = conv_parse_surge(source, document, error);
    else { ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "format must be auto, mihomo, or surge"); status = CH_ERROR_INVALID_ARGUMENT; }
    if (status == CH_OK) status = conv_render(document, out_toml, error);
    if (status == CH_OK) status = conv_sha256(*out_toml, sha, error);
    ch_json_value_destroy(request);
    return status;
}

static char *conv_review_json(const conv_document *document, const char *toml,
                              const char *sha, ch_error *error) {
    size_t chains = 0U; for (size_t index = 0U; index < document->proxy_count; ++index)
        if (document->proxies[index].supported) ++chains;
    ch_json_buffer json; ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"format\":") && ch_json_append_string(&json, document->format) &&
        ch_json_append(&json, ",\"sha256\":") && ch_json_append_string(&json, sha) &&
        ch_json_append(&json, ",\"profiles\":[{\"source_name\":") &&
        ch_json_append_string(&json, document->profile_name) &&
        ch_json_append(&json, ",\"suggested_name\":") && ch_json_append_string(&json, document->profile_name) &&
        ch_json_append_format(&json, ",\"chain_count\":%zu,\"group_count\":%zu,\"rule_count\":%zu}],\"warnings\":[",
                              chains, document->group_count, document->rules.count);
    for (size_t index = 0U; okay && index < document->warnings.count; ++index)
        okay = (index == 0U || ch_json_append(&json, ",")) && ch_json_append(&json, document->warnings.items[index]);
    okay = okay && ch_json_append(&json, "],\"toml\":") && ch_json_append_string(&json, toml) && ch_json_append(&json, "}");
    char *result = okay ? ch_json_take(&json) : NULL; ch_json_dispose(&json);
    if (result == NULL) ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode converter review");
    return result;
}

ch_status ch_profile_converter_review_request_json(const char *request_json,
                                                    char **out_json,
                                                    ch_error *error) {
    ch_error_clear(error);
    if (out_json == NULL) { ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "review output is required"); return CH_ERROR_INVALID_ARGUMENT; }
    *out_json = NULL; conv_document document = {0}; char *toml = NULL; char sha[65];
    ch_status status = conv_request(request_json, &document, &toml, sha, error);
    if (status == CH_OK) {
        *out_json = conv_review_json(&document, toml, sha, error);
        if (*out_json == NULL) status = error->code;
    }
    free(toml); conv_document_clear(&document); return status;
}

ch_status ch_profile_converter_import_request_json(
    const ch_config *current, const char *request_json, char **out_toml,
    char **out_json, ch_error *error) {
    ch_error_clear(error);
    if (current == NULL || out_toml == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "current config and outputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_toml = NULL; *out_json = NULL;
    conv_document document = {0}; char *converted = NULL; char sha[65];
    ch_status status = conv_request(request_json, &document, &converted, sha, error);
    ch_json_value *request = status == CH_OK ? ch_json_parse(request_json, strlen(request_json), error) : NULL;
    const char *expected = request == NULL ? NULL : ch_json_string_value(ch_json_object_get(request, "expected_sha256"));
    bool activate = request != NULL && ch_json_bool_value(ch_json_object_get(request, "activate"), false);
    if (status == CH_OK && (expected == NULL || strcmp(expected, sha) != 0)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "conversion changed after review"); status = CH_ERROR_INVALID_ARGUMENT;
    }
    ch_json_buffer merge; ch_json_init(&merge);
    if (status == CH_OK) {
        int okay = ch_json_append(&merge, "{\"import_text\":") && ch_json_append_string(&merge, converted) &&
            ch_json_append(&merge, ",\"profiles\":[{\"source_name\":") && ch_json_append_string(&merge, document.profile_name) &&
            ch_json_append(&merge, ",\"target_name\":") && ch_json_append_string(&merge, document.profile_name) &&
            ch_json_append(&merge, "}],\"activate_profile\":") && ch_json_append_string(&merge, activate ? document.profile_name : "") &&
            ch_json_append(&merge, "}");
        char *merge_json = okay ? ch_json_take(&merge) : NULL;
        if (merge_json == NULL) { ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode converter merge"); status = CH_ERROR_OUT_OF_MEMORY; }
        else status = ch_config_merge_reviewed_import_document(current, merge_json, out_toml, error);
        free(merge_json);
    }
    ch_json_dispose(&merge);
    if (status == CH_OK) *out_json = conv_review_json(&document, converted, sha, error);
    if (status == CH_OK && *out_json == NULL) status = error->code;
    ch_json_value_destroy(request); free(converted); conv_document_clear(&document);
    return status;
}
