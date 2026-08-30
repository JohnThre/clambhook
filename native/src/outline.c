// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "outline.h"

#include <ctype.h>
#include <curl/curl.h>
#include <errno.h>
#include <inttypes.h>
#include <openssl/evp.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>
#include <yaml.h>

#include "clambhook/json.h"
#include "http_safety.h"
#include "internal.h"
#include "protocol_shadowsocks.h"

#define CH_OUTLINE_MAX_BODY (64U * 1024U)
#define CH_OUTLINE_MAX_PREFIX 32U

typedef struct outline_endpoint {
    char *address;
    char *method;
    char *password;
    uint8_t prefix[CH_OUTLINE_MAX_PREFIX];
    size_t prefix_length;
} outline_endpoint;

typedef struct outline_key {
    outline_endpoint tcp;
    outline_endpoint udp;
    char *suggested_name;
    char *dynamic_key;
    int dynamic;
    int64_t refreshed_at_ns;
} outline_key;

typedef struct outline_buffer {
    char *data;
    size_t length;
    size_t capacity;
    int overflow;
} outline_buffer;

static void outline_endpoint_clear(outline_endpoint *endpoint) {
    if (endpoint == NULL) return;
    free(endpoint->address);
    free(endpoint->method);
    free(endpoint->password);
    memset(endpoint, 0, sizeof(*endpoint));
}

static void outline_key_clear(outline_key *key) {
    if (key == NULL) return;
    outline_endpoint_clear(&key->tcp);
    outline_endpoint_clear(&key->udp);
    free(key->suggested_name);
    free(key->dynamic_key);
    memset(key, 0, sizeof(*key));
}

static char *outline_trim_copy(const char *input) {
    if (input == NULL) return NULL;
    while (*input != '\0' && isspace((unsigned char)*input) != 0) ++input;
    const char *end = input + strlen(input);
    while (end > input && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - input);
    char *result = malloc(length + 1U);
    if (result != NULL) {
        memcpy(result, input, length);
        result[length] = '\0';
    }
    return result;
}

static int64_t outline_now_ns(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_REALTIME, &value) != 0) return 0;
    return (int64_t)value.tv_sec * INT64_C(1000000000) +
        (int64_t)value.tv_nsec;
}

static char *outline_percent_decode(const char *input, size_t length,
                                    int plus_space, ch_error *error) {
    char *result = malloc(length + 1U);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Outline URL component");
        return NULL;
    }
    size_t output = 0U;
    for (size_t index = 0U; index < length; ++index) {
        unsigned char value = (unsigned char)input[index];
        if (value == '%' && index + 2U < length &&
            isxdigit((unsigned char)input[index + 1U]) != 0 &&
            isxdigit((unsigned char)input[index + 2U]) != 0) {
            char hex[3] = {input[index + 1U], input[index + 2U], '\0'};
            value = (unsigned char)strtoul(hex, NULL, 16);
            index += 2U;
        } else if (value == '%') {
            free(result);
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline URL contains invalid percent encoding");
            return NULL;
        } else if (value == '+' && plus_space != 0) {
            value = ' ';
        }
        if (value == '\0') {
            free(result);
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline URL contains an embedded NUL");
            return NULL;
        }
        result[output++] = (char)value;
    }
    result[output] = '\0';
    return result;
}

static ch_status outline_base64_decode(const char *input, uint8_t **out,
                                       size_t *out_length,
                                       ch_error *error) {
    size_t length = strlen(input);
    if (length == 0U || length > 16384U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid Outline Base64 value");
        return CH_ERROR_PARSE;
    }
    size_t padded = (length + 3U) & ~(size_t)3U;
    char *normalized = malloc(padded + 1U);
    uint8_t *decoded = malloc((padded / 4U) * 3U + 1U);
    if (normalized == NULL || decoded == NULL) {
        free(normalized); free(decoded);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Outline Base64 value");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < padded; ++index) {
        char value = index < length ? input[index] : '=';
        normalized[index] = value == '-' ? '+' : (value == '_' ? '/' : value);
    }
    normalized[padded] = '\0';
    int count = EVP_DecodeBlock(decoded, (const unsigned char *)normalized,
                                (int)padded);
    free(normalized);
    if (count < 0) {
        free(decoded);
        ch_error_set(error, CH_ERROR_PARSE, "invalid Outline Base64 value");
        return CH_ERROR_PARSE;
    }
    size_t actual = (size_t)count;
    for (size_t index = padded; index > 0U && input[index - 1U] == '=';
         --index) {
        if (actual > 0U) --actual;
    }
    if (padded > length) actual -= padded - length;
    decoded[actual] = '\0';
    *out = decoded;
    *out_length = actual;
    return CH_OK;
}

static char *outline_base64_encode(const uint8_t *input, size_t length,
                                   ch_error *error) {
    if (length == 0U) return ch_strdup("");
    size_t capacity = 4U * ((length + 2U) / 3U) + 1U;
    char *result = malloc(capacity);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode Outline prefix");
        return NULL;
    }
    int count = EVP_EncodeBlock((unsigned char *)result, input, (int)length);
    if (count < 0) {
        free(result);
        ch_error_set(error, CH_ERROR_INTERNAL, "encode Outline prefix");
        return NULL;
    }
    result[(size_t)count] = '\0';
    return result;
}

static ch_status outline_prefix_from_utf8_bytes(const uint8_t *input,
                                                size_t input_length,
                                                outline_endpoint *endpoint,
                                                ch_error *error) {
    endpoint->prefix_length = 0U;
    const uint8_t *cursor = input;
    const uint8_t *end = input + input_length;
    while (cursor < end) {
        uint32_t codepoint;
        size_t count;
        if (*cursor < 0x80U) {
            codepoint = *cursor; count = 1U;
        } else if ((*cursor & 0xe0U) == 0xc0U && cursor + 1U < end &&
                   (cursor[1] & 0xc0U) == 0x80U) {
            codepoint = ((uint32_t)(cursor[0] & 0x1fU) << 6U) |
                (uint32_t)(cursor[1] & 0x3fU);
            count = 2U;
            if (codepoint < 0x80U) codepoint = UINT32_MAX;
        } else {
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline prefix contains a character outside U+00FF");
            return CH_ERROR_PARSE;
        }
        if (codepoint > 0xffU) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline prefix contains a character outside U+00FF");
            return CH_ERROR_PARSE;
        }
        if (endpoint->prefix_length >= sizeof(endpoint->prefix)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "Outline prefix exceeds 32 bytes");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        endpoint->prefix[endpoint->prefix_length++] = (uint8_t)codepoint;
        cursor += count;
    }
    return CH_OK;
}

static ch_status outline_prefix_from_utf8(const char *input,
                                          outline_endpoint *endpoint,
                                          ch_error *error) {
    const char *value = input == NULL ? "" : input;
    return outline_prefix_from_utf8_bytes((const uint8_t *)value,
                                          strlen(value), endpoint, error);
}

static ch_status outline_query_prefix(const char *query,
                                      outline_endpoint *endpoint,
                                      int *found, ch_error *error) {
    *found = 0;
    if (query == NULL) return CH_OK;
    const char *cursor = query;
    while (*cursor != '\0' && *cursor != '#') {
        const char *end = strchr(cursor, '&');
        const char *fragment = strchr(cursor, '#');
        if (end == NULL || (fragment != NULL && fragment < end)) end = fragment;
        if (end == NULL) end = cursor + strlen(cursor);
        const char *equal = memchr(cursor, '=', (size_t)(end - cursor));
        if (equal != NULL && (size_t)(equal - cursor) == 6U &&
            memcmp(cursor, "prefix", 6U) == 0) {
            const char *value = equal + 1U;
            size_t encoded_length = (size_t)(end - value);
            uint8_t *decoded = malloc(encoded_length + 1U);
            if (decoded == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "decode Outline prefix");
                return CH_ERROR_OUT_OF_MEMORY;
            }
            size_t output = 0U;
            for (size_t index = 0U; index < encoded_length; ++index) {
                unsigned char byte = (unsigned char)value[index];
                if (byte == '%' && index + 2U < encoded_length &&
                    isxdigit((unsigned char)value[index + 1U]) != 0 &&
                    isxdigit((unsigned char)value[index + 2U]) != 0) {
                    char hex[3] = {value[index + 1U], value[index + 2U], '\0'};
                    byte = (unsigned char)strtoul(hex, NULL, 16);
                    index += 2U;
                } else if (byte == '%') {
                    free(decoded);
                    ch_error_set(error, CH_ERROR_PARSE,
                                 "Outline URL contains invalid percent encoding");
                    return CH_ERROR_PARSE;
                } else if (byte == '+') {
                    byte = ' ';
                }
                decoded[output++] = byte;
            }
            ch_status status = outline_prefix_from_utf8_bytes(
                decoded, output, endpoint, error);
            free(decoded);
            *found = 1;
            return status;
        }
        cursor = *end == '\0' || *end == '#' ? end : end + 1U;
    }
    return CH_OK;
}

static ch_status outline_validate_endpoint(outline_endpoint *endpoint,
                                           ch_error *error) {
    if (endpoint->address == NULL || endpoint->address[0] == '\0' ||
        endpoint->method == NULL || endpoint->method[0] == '\0' ||
        endpoint->password == NULL || endpoint->password[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline endpoint requires address, cipher, and secret");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *separator = endpoint->address[0] == '[' ?
        strrchr(endpoint->address, ']') : strrchr(endpoint->address, ':');
    if (separator == NULL || (endpoint->address[0] == '[' &&
        separator[1] != ':')) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline endpoint must be host:port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *port_text = endpoint->address[0] == '[' ? separator + 2U :
        separator + 1U;
    char *end = NULL;
    errno = 0;
    long port = strtol(port_text, &end, 10);
    if (errno != 0 || end == port_text || *end != '\0' || port < 1L ||
        port > 65535L) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline endpoint port is invalid");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_ss_cipher cipher;
    ch_status status = ch_ss_cipher_from_name(endpoint->method, &cipher,
                                              error);
    if (status != CH_OK) return status;
    if (endpoint->prefix_length > cipher.salt_size) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline prefix is longer than the cipher salt");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return CH_OK;
}

static ch_status outline_copy_endpoint(const outline_endpoint *source,
                                       outline_endpoint *target,
                                       ch_error *error) {
    target->address = ch_strdup(source->address);
    target->method = ch_strdup(source->method);
    target->password = ch_strdup(source->password);
    target->prefix_length = source->prefix_length;
    memcpy(target->prefix, source->prefix, source->prefix_length);
    if (target->address == NULL || target->method == NULL ||
        target->password == NULL) {
        outline_endpoint_clear(target);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline endpoint");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static char *outline_query_value(const char *query, const char *name,
                                 ch_error *error) {
    if (query == NULL) return NULL;
    size_t name_length = strlen(name);
    const char *cursor = query;
    while (*cursor != '\0') {
        const char *end = strchr(cursor, '&');
        const char *fragment = strchr(cursor, '#');
        if (end == NULL || (fragment != NULL && fragment < end)) end = fragment;
        if (end == NULL) end = cursor + strlen(cursor);
        const char *equal = memchr(cursor, '=', (size_t)(end - cursor));
        size_t key_length = equal == NULL ? (size_t)(end - cursor) :
            (size_t)(equal - cursor);
        if (key_length == name_length &&
            strncmp(cursor, name, name_length) == 0) {
            const char *value = equal == NULL ? end : equal + 1U;
            return outline_percent_decode(value, (size_t)(end - value), 1,
                                          error);
        }
        cursor = *end == '\0' ? end : end + 1U;
    }
    return NULL;
}

static ch_status outline_parse_ss(const char *raw, outline_key *key,
                                  ch_error *error) {
    const char *value = raw + 5U;
    const char *fragment = strchr(value, '#');
    const char *query = strchr(value, '?');
    const char *end = value + strlen(value);
    if (fragment != NULL && fragment < end) end = fragment;
    if (query != NULL && query < end) end = query;
    if (query != NULL && fragment != NULL && query > fragment) query = NULL;
    if (query != NULL) {
        char *plugin = outline_query_value(query + 1U, "plugin", error);
        if (plugin != NULL) {
            free(plugin);
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "Outline Shadowsocks plugins are not supported");
            return CH_ERROR_UNSUPPORTED;
        }
    }
    if (fragment != NULL && fragment[1] != '\0' &&
        key->suggested_name == NULL) {
        key->suggested_name = outline_percent_decode(
            fragment + 1U, strlen(fragment + 1U), 0, error);
        if (key->suggested_name == NULL) return error->code;
    }
    size_t authority_length = (size_t)(end - value);
    char *authority = malloc(authority_length + 1U);
    if (authority == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline access key");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(authority, value, authority_length);
    authority[authority_length] = '\0';
    while (authority_length > 0U && authority[authority_length - 1U] == '/') {
        authority[--authority_length] = '\0';
    }
    char *at = strrchr(authority, '@');
    if (at == NULL) {
        uint8_t *decoded = NULL;
        size_t decoded_length = 0U;
        ch_status status = outline_base64_decode(authority, &decoded,
                                                 &decoded_length, error);
        free(authority);
        if (status != CH_OK) return status;
        if (memchr(decoded, '\0', decoded_length) != NULL) {
            free(decoded);
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline access key contains an embedded NUL");
            return CH_ERROR_PARSE;
        }
        const char *suffix = query != NULL ? query :
            (fragment != NULL ? fragment : "");
        size_t suffix_length = strlen(suffix);
        size_t nested_length = decoded_length + suffix_length + 6U;
        char *nested = malloc(nested_length);
        if (nested == NULL) {
            free(decoded);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "decode Outline access key");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        (void)snprintf(nested, nested_length, "ss://%s%s",
                       (char *)decoded, suffix);
        free(decoded);
        status = outline_parse_ss(nested, key, error);
        free(nested);
        return status;
    }
    *at = '\0';
    char *userinfo = outline_percent_decode(authority, strlen(authority), 0,
                                            error);
    if (userinfo == NULL) { free(authority); return error->code; }
    char *cipher_info = userinfo;
    uint8_t *decoded = NULL;
    size_t decoded_length = 0U;
    ch_error ignored;
    if (outline_base64_decode(userinfo, &decoded, &decoded_length,
                              &ignored) == CH_OK &&
        memchr(decoded, ':', decoded_length) != NULL &&
        memchr(decoded, '\0', decoded_length) == NULL) {
        cipher_info = (char *)decoded;
    }
    char *colon = strchr(cipher_info, ':');
    if (colon == NULL) {
        free(decoded); free(userinfo); free(authority);
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline access key has no cipher separator");
        return CH_ERROR_PARSE;
    }
    *colon = '\0';
    key->tcp.method = ch_strdup(cipher_info);
    key->tcp.password = ch_strdup(colon + 1U);
    key->tcp.address = ch_strdup(at + 1U);
    free(decoded); free(userinfo); free(authority);
    if (key->tcp.method == NULL || key->tcp.password == NULL ||
        key->tcp.address == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline access key fields");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (query != NULL) {
        int found = 0;
        ch_status prefix_status = outline_query_prefix(
            query + 1U, &key->tcp, &found, error);
        if (prefix_status != CH_OK) return prefix_status;
    }
    ch_status status = outline_validate_endpoint(&key->tcp, error);
    if (status == CH_OK) status = outline_copy_endpoint(&key->tcp, &key->udp,
                                                        error);
    return status;
}

static const ch_json_value *outline_json_member(const ch_json_value *object,
                                                const char *name) {
    return ch_json_object_get(object, name);
}

static char *outline_json_string_copy(const ch_json_value *object,
                                      const char *name) {
    const char *value = ch_json_string_value(outline_json_member(object,
                                                                  name));
    return value == NULL ? NULL : ch_strdup(value);
}

static ch_status outline_parse_legacy_json(const char *body,
                                           outline_key *key,
                                           ch_error *error) {
    ch_json_value *root = ch_json_parse(body, strlen(body), error);
    if (root == NULL) return error->code;
    key->tcp.address = outline_json_string_copy(root, "server");
    key->tcp.method = outline_json_string_copy(root, "method");
    key->tcp.password = outline_json_string_copy(root, "password");
    int64_t port = 0;
    const ch_json_value *port_value = outline_json_member(root, "server_port");
    if (port_value != NULL &&
        !ch_json_int64_value(port_value, &port)) {
        const char *port_string = ch_json_string_value(port_value);
        if (port_string != NULL) port = strtoll(port_string, NULL, 10);
    }
    if (key->tcp.address != NULL && port > 0 && port <= 65535) {
        const char *host = key->tcp.address;
        size_t length = strlen(host) + 16U;
        char *address = malloc(length);
        if (address != NULL) {
            (void)snprintf(address, length, strchr(host, ':') == NULL ?
                "%s:%" PRId64 : "[%s]:%" PRId64, host, port);
        }
        free(key->tcp.address);
        key->tcp.address = address;
    }
    const char *prefix = ch_json_string_value(outline_json_member(root,
                                                                   "prefix"));
    ch_status status = prefix == NULL ? CH_OK :
        outline_prefix_from_utf8(prefix, &key->tcp, error);
    ch_json_value_destroy(root);
    if (status == CH_OK) status = outline_validate_endpoint(&key->tcp, error);
    if (status == CH_OK) status = outline_copy_endpoint(&key->tcp, &key->udp,
                                                        error);
    return status;
}

static const yaml_node_t *outline_yaml_node(yaml_document_t *document,
                                            int index) {
    return index <= 0 ? NULL : yaml_document_get_node(document, index);
}

static int outline_yaml_scalar_equal(const yaml_node_t *node,
                                     const char *value) {
    return node != NULL && node->type == YAML_SCALAR_NODE &&
        strcmp((const char *)node->data.scalar.value, value) == 0;
}

static const yaml_node_t *outline_yaml_mapping_value(
    yaml_document_t *document, const yaml_node_t *mapping, const char *name,
    unsigned depth, int *duplicates) {
    if (mapping == NULL || mapping->type != YAML_MAPPING_NODE || depth > 16U) {
        return NULL;
    }
    const yaml_node_t *result = NULL;
    for (yaml_node_pair_t *pair = mapping->data.mapping.pairs.start;
         pair < mapping->data.mapping.pairs.top; ++pair) {
        const yaml_node_t *key = outline_yaml_node(document, pair->key);
        const yaml_node_t *value = outline_yaml_node(document, pair->value);
        if (outline_yaml_scalar_equal(key, name)) {
            if (result != NULL && duplicates != NULL) *duplicates = 1;
            result = value;
        }
    }
    if (result != NULL) return result;
    for (yaml_node_pair_t *pair = mapping->data.mapping.pairs.start;
         pair < mapping->data.mapping.pairs.top; ++pair) {
        const yaml_node_t *key = outline_yaml_node(document, pair->key);
        const yaml_node_t *value = outline_yaml_node(document, pair->value);
        if (!outline_yaml_scalar_equal(key, "<<")) continue;
        if (value != NULL && value->type == YAML_MAPPING_NODE) {
            result = outline_yaml_mapping_value(document, value, name,
                                                depth + 1U, duplicates);
        } else if (value != NULL && value->type == YAML_SEQUENCE_NODE) {
            for (yaml_node_item_t *item = value->data.sequence.items.start;
                 result == NULL && item < value->data.sequence.items.top;
                 ++item) {
                result = outline_yaml_mapping_value(
                    document, outline_yaml_node(document, *item), name,
                    depth + 1U, duplicates);
            }
        }
    }
    return result;
}

static ch_status outline_yaml_reject_duplicate_keys(
    yaml_document_t *document, const yaml_node_t *node, unsigned depth,
    ch_error *error) {
    if (node == NULL || depth > 64U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline YAML aliases are too deeply nested");
        return CH_ERROR_PARSE;
    }
    if (node->type == YAML_SEQUENCE_NODE) {
        for (yaml_node_item_t *item = node->data.sequence.items.start;
             item < node->data.sequence.items.top; ++item) {
            ch_status status = outline_yaml_reject_duplicate_keys(
                document, outline_yaml_node(document, *item), depth + 1U,
                error);
            if (status != CH_OK) return status;
        }
        return CH_OK;
    }
    if (node->type != YAML_MAPPING_NODE) return CH_OK;
    for (yaml_node_pair_t *outer = node->data.mapping.pairs.start;
         outer < node->data.mapping.pairs.top; ++outer) {
        const yaml_node_t *outer_key = outline_yaml_node(document, outer->key);
        if (outer_key == NULL || outer_key->type != YAML_SCALAR_NODE) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline YAML object keys must be strings");
            return CH_ERROR_PARSE;
        }
        for (yaml_node_pair_t *inner = outer + 1U;
             inner < node->data.mapping.pairs.top; ++inner) {
            const yaml_node_t *inner_key = outline_yaml_node(document,
                                                              inner->key);
            if (inner_key != NULL && inner_key->type == YAML_SCALAR_NODE &&
                outer_key->data.scalar.length == inner_key->data.scalar.length &&
                memcmp(outer_key->data.scalar.value,
                       inner_key->data.scalar.value,
                       outer_key->data.scalar.length) == 0) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "Outline YAML contains a duplicate key");
                return CH_ERROR_PARSE;
            }
        }
        ch_status status = outline_yaml_reject_duplicate_keys(
            document, outline_yaml_node(document, outer->value), depth + 1U,
            error);
        if (status != CH_OK) return status;
    }
    return CH_OK;
}

static char *outline_yaml_scalar_copy(yaml_document_t *document,
                                      const yaml_node_t *mapping,
                                      const char *name, int required,
                                      ch_error *error) {
    int duplicates = 0;
    const yaml_node_t *node = outline_yaml_mapping_value(
        document, mapping, name, 0U, &duplicates);
    if (duplicates != 0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline YAML contains duplicate %s fields", name);
        return NULL;
    }
    if (node == NULL && required == 0) return NULL;
    if (node == NULL || node->type != YAML_SCALAR_NODE) {
        ch_error_set(error, node == NULL ? CH_ERROR_PARSE :
                     CH_ERROR_UNSUPPORTED,
                     node == NULL ? "Outline YAML is missing %s" :
                     "advanced Outline %s values are not supported", name);
        return NULL;
    }
    char *copy = ch_strdup((const char *)node->data.scalar.value);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline YAML %s", name);
    }
    return copy;
}

static ch_status outline_yaml_prefix(yaml_document_t *document,
                                     const yaml_node_t *mapping,
                                     outline_endpoint *endpoint,
                                     ch_error *error) {
    int duplicates = 0;
    const yaml_node_t *node = outline_yaml_mapping_value(
        document, mapping, "prefix", 0U, &duplicates);
    if (duplicates != 0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline YAML contains duplicate prefix fields");
        return CH_ERROR_PARSE;
    }
    if (node == NULL) return CH_OK;
    if (node->type != YAML_SCALAR_NODE) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "advanced Outline prefix values are not supported");
        return CH_ERROR_UNSUPPORTED;
    }
    return outline_prefix_from_utf8_bytes(node->data.scalar.value,
                                          node->data.scalar.length,
                                          endpoint, error);
}

static ch_status outline_parse_yaml_endpoint(yaml_document_t *document,
                                             const yaml_node_t *mapping,
                                             outline_endpoint *endpoint,
                                             ch_error *error) {
    char *type = outline_yaml_scalar_copy(document, mapping, "$type", 1,
                                          error);
    if (type == NULL) return error->code;
    if (strcmp(type, "shadowsocks") != 0) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "advanced Outline %s transport is not supported", type);
        free(type);
        return CH_ERROR_UNSUPPORTED;
    }
    free(type);
    endpoint->address = outline_yaml_scalar_copy(document, mapping,
                                                 "endpoint", 1, error);
    endpoint->method = outline_yaml_scalar_copy(document, mapping, "cipher",
                                                1, error);
    endpoint->password = outline_yaml_scalar_copy(document, mapping, "secret",
                                                  1, error);
    if (endpoint->address == NULL || endpoint->method == NULL ||
        endpoint->password == NULL || error->code != CH_OK) {
        return error->code == CH_OK ? CH_ERROR_PARSE : error->code;
    }
    ch_status status = outline_yaml_prefix(document, mapping, endpoint, error);
    if (status == CH_OK) status = outline_validate_endpoint(endpoint, error);
    return status;
}

static ch_status outline_parse_yaml(const char *body, outline_key *key,
                                    ch_error *error) {
    yaml_parser_t parser;
    yaml_document_t document;
    memset(&document, 0, sizeof(document));
    if (!yaml_parser_initialize(&parser)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize Outline YAML parser");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    yaml_parser_set_input_string(&parser, (const unsigned char *)body,
                                 strlen(body));
    if (!yaml_parser_load(&parser, &document)) {
        ch_error_set(error, CH_ERROR_PARSE, "parse Outline YAML: %s",
                     parser.problem == NULL ? "invalid document" :
                                              parser.problem);
        yaml_parser_delete(&parser);
        return CH_ERROR_PARSE;
    }
    yaml_parser_delete(&parser);
    const yaml_node_t *root = yaml_document_get_root_node(&document);
    if (root == NULL || root->type != YAML_MAPPING_NODE) {
        yaml_document_delete(&document);
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline YAML root must be an object");
        return CH_ERROR_PARSE;
    }
    ch_status status = outline_yaml_reject_duplicate_keys(
        &document, root, 0U, error);
    if (status != CH_OK) {
        yaml_document_delete(&document);
        return status;
    }
    int duplicates = 0;
    const yaml_node_t *transport = outline_yaml_mapping_value(
        &document, root, "transport", 0U, &duplicates);
    status = CH_OK;
    if (duplicates != 0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline YAML contains duplicate transport fields");
        status = CH_ERROR_PARSE;
    } else if (transport == NULL) {
        key->tcp.address = outline_yaml_scalar_copy(&document, root, "server",
                                                    1, error);
        key->tcp.method = outline_yaml_scalar_copy(&document, root, "method",
                                                   1, error);
        key->tcp.password = outline_yaml_scalar_copy(
            &document, root, "password", 1, error);
        char *port = outline_yaml_scalar_copy(&document, root, "server_port",
                                              1, error);
        if (error->code != CH_OK || key->tcp.address == NULL || port == NULL) {
            status = error->code == CH_OK ? CH_ERROR_PARSE : error->code;
        } else {
            char *host = key->tcp.address;
            size_t length = strlen(host) + strlen(port) + 4U;
            key->tcp.address = malloc(length);
            if (key->tcp.address == NULL) {
                status = CH_ERROR_OUT_OF_MEMORY;
                ch_error_set(error, status, "copy Outline YAML endpoint");
            } else {
                (void)snprintf(key->tcp.address, length,
                    strchr(host, ':') == NULL ? "%s:%s" : "[%s]:%s",
                    host, port);
            }
            free(host);
        }
        if (status == CH_OK) status = outline_yaml_prefix(
            &document, root, &key->tcp, error);
        free(port);
        if (status == CH_OK) status = outline_validate_endpoint(&key->tcp,
                                                                 error);
        if (status == CH_OK) status = outline_copy_endpoint(&key->tcp,
                                                             &key->udp,
                                                             error);
    } else if (transport->type != YAML_MAPPING_NODE) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "advanced Outline transport values are not supported");
        status = CH_ERROR_UNSUPPORTED;
    } else {
        char *type = outline_yaml_scalar_copy(&document, transport, "$type",
                                              1, error);
        if (type == NULL) {
            status = error->code;
        } else if (strcmp(type, "tcpudp") != 0) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "advanced Outline %s transport is not supported",
                         type);
            status = CH_ERROR_UNSUPPORTED;
        }
        free(type);
        const yaml_node_t *tcp = status == CH_OK ?
            outline_yaml_mapping_value(&document, transport, "tcp", 0U,
                                       &duplicates) : NULL;
        const yaml_node_t *udp = status == CH_OK ?
            outline_yaml_mapping_value(&document, transport, "udp", 0U,
                                       &duplicates) : NULL;
        if (status == CH_OK && (duplicates != 0 || tcp == NULL || udp == NULL)) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "Outline tcpudp YAML requires unique tcp and udp fields");
            status = CH_ERROR_PARSE;
        }
        if (status == CH_OK) status = outline_parse_yaml_endpoint(
            &document, tcp, &key->tcp, error);
        if (status == CH_OK) status = outline_parse_yaml_endpoint(
            &document, udp, &key->udp, error);
    }
    yaml_document_delete(&document);
    return status;
}

static size_t outline_http_write(char *data, size_t size, size_t count,
                                 void *opaque) {
    outline_buffer *buffer = opaque;
    if (size != 0U && count > SIZE_MAX / size) {
        buffer->overflow = 1;
        return 0U;
    }
    size_t length = size * count;
    if (length > CH_OUTLINE_MAX_BODY - buffer->length) {
        buffer->overflow = 1;
        return 0U;
    }
    size_t needed = buffer->length + length + 1U;
    if (needed > buffer->capacity) {
        size_t capacity = buffer->capacity == 0U ? 4096U : buffer->capacity;
        while (capacity < needed) capacity *= 2U;
        char *grown = realloc(buffer->data, capacity);
        if (grown == NULL) return 0U;
        buffer->data = grown;
        buffer->capacity = capacity;
    }
    memcpy(buffer->data + buffer->length, data, length);
    buffer->length += length;
    buffer->data[buffer->length] = '\0';
    return length;
}

typedef struct outline_headers { char *location; } outline_headers;

static pthread_once_t outline_curl_once = PTHREAD_ONCE_INIT;
static CURLcode outline_curl_init_status = CURLE_FAILED_INIT;

static void outline_curl_initialize(void) {
    outline_curl_init_status = curl_global_init(CURL_GLOBAL_DEFAULT);
}

static size_t outline_http_header(char *data, size_t size, size_t count,
                                  void *opaque) {
    outline_headers *headers = opaque;
    if (size != 0U && count > SIZE_MAX / size) return 0U;
    size_t length = size * count;
    static const char prefix[] = "Location:";
    if (length > sizeof(prefix) - 1U &&
        strncasecmp(data, prefix, sizeof(prefix) - 1U) == 0) {
        const char *value = data + sizeof(prefix) - 1U;
        const char *end = data + length;
        while (value < end && isspace((unsigned char)*value) != 0) ++value;
        while (end > value && isspace((unsigned char)end[-1]) != 0) --end;
        char *copy = malloc((size_t)(end - value) + 1U);
        if (copy == NULL) return 0U;
        memcpy(copy, value, (size_t)(end - value));
        copy[end - value] = '\0';
        free(headers->location);
        headers->location = copy;
    }
    return length;
}

static ch_status outline_fetch_dynamic(const char *dynamic_key,
                                       char **out_body, ch_error *error) {
    (void)pthread_once(&outline_curl_once, outline_curl_initialize);
    if (outline_curl_init_status != CURLE_OK) {
        ch_error_set(error, CH_ERROR_IO,
                     "initialize Outline HTTPS retrieval");
        return CH_ERROR_IO;
    }
    size_t length = strlen(dynamic_key);
    if (strncasecmp(dynamic_key, "ssconf://", 9U) != 0 || length <= 9U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "dynamic Outline key must use ssconf://");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *url = malloc(length + 1U);
    if (url == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate dynamic Outline URL");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(url, length + 1U, "https://%s", dynamic_key + 9U);
    char *fragment = strchr(url, '#');
    if (fragment != NULL) *fragment = '\0';
    ch_http_endpoint initial = {0};
    ch_http_endpoint endpoint = {0};
    ch_status status = ch_http_endpoint_prepare(url, "Outline key", &initial,
                                                error);
    if (status == CH_OK) status = ch_http_endpoint_prepare(
        url, "Outline key", &endpoint, error);
    outline_buffer body = {0};
    long http_status = 0L;
    for (unsigned redirect = 0U; status == CH_OK; ++redirect) {
        CURL *curl = curl_easy_init();
        struct curl_slist *resolve = NULL;
        outline_headers headers = {0};
        char curl_error[CURL_ERROR_SIZE] = {0};
        if (curl != NULL) resolve = curl_slist_append(resolve,
                                                       endpoint.resolve);
        if (curl == NULL || resolve == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate Outline HTTP request");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        free(body.data); memset(&body, 0, sizeof(body));
        CURLcode code = CURLE_OK;
#define OUTLINE_CURL_SET(option, value) do { \
    if (status == CH_OK && code == CURLE_OK) \
        code = curl_easy_setopt(curl, (option), (value)); \
} while (0)
        OUTLINE_CURL_SET(CURLOPT_URL, endpoint.url);
        OUTLINE_CURL_SET(CURLOPT_RESOLVE, resolve);
        OUTLINE_CURL_SET(CURLOPT_PROXY, "");
#if LIBCURL_VERSION_NUM >= 0x075500
        OUTLINE_CURL_SET(CURLOPT_PROTOCOLS_STR, "https");
#else
        OUTLINE_CURL_SET(CURLOPT_PROTOCOLS, (long)CURLPROTO_HTTPS);
#endif
        OUTLINE_CURL_SET(CURLOPT_FOLLOWLOCATION, 0L);
        OUTLINE_CURL_SET(CURLOPT_TIMEOUT_MS, 15000L);
        OUTLINE_CURL_SET(CURLOPT_CONNECTTIMEOUT_MS, 15000L);
        OUTLINE_CURL_SET(CURLOPT_NOSIGNAL, 1L);
        OUTLINE_CURL_SET(CURLOPT_FRESH_CONNECT, 1L);
        OUTLINE_CURL_SET(CURLOPT_FORBID_REUSE, 1L);
        OUTLINE_CURL_SET(CURLOPT_DNS_CACHE_TIMEOUT, 0L);
        OUTLINE_CURL_SET(CURLOPT_USERAGENT, "clambhook/1 outline-key");
        OUTLINE_CURL_SET(CURLOPT_WRITEFUNCTION, outline_http_write);
        OUTLINE_CURL_SET(CURLOPT_WRITEDATA, &body);
        OUTLINE_CURL_SET(CURLOPT_HEADERFUNCTION, outline_http_header);
        OUTLINE_CURL_SET(CURLOPT_HEADERDATA, &headers);
        OUTLINE_CURL_SET(CURLOPT_ERRORBUFFER, curl_error);
#undef OUTLINE_CURL_SET
        if (status == CH_OK && code == CURLE_OK) code = curl_easy_perform(curl);
        if (status == CH_OK && code == CURLE_OK) {
            code = curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE,
                                     &http_status);
        }
        if (status == CH_OK && code != CURLE_OK) {
            ch_error_set(error, body.overflow ? CH_ERROR_INVALID_ARGUMENT :
                         CH_ERROR_IO, body.overflow ?
                         "Outline configuration exceeds 64 KiB" :
                         "fetch Outline configuration: %s",
                         body.overflow ? "" : (curl_error[0] == '\0' ?
                         curl_easy_strerror(code) : curl_error));
            status = body.overflow ? CH_ERROR_INVALID_ARGUMENT : CH_ERROR_IO;
        }
        curl_slist_free_all(resolve);
        curl_easy_cleanup(curl);
        int redirecting = http_status == 301L || http_status == 302L ||
            http_status == 303L || http_status == 307L || http_status == 308L;
        if (status != CH_OK || !redirecting) {
            free(headers.location);
            break;
        }
        if (redirect >= 9U || headers.location == NULL) {
            free(headers.location);
            ch_error_set(error, CH_ERROR_IO,
                         "Outline configuration redirect is invalid");
            status = CH_ERROR_IO;
            break;
        }
        char *next_url = ch_http_resolve_redirect(endpoint.url,
                                                  headers.location);
        free(headers.location);
        ch_http_endpoint next = {0};
        status = next_url == NULL ? CH_ERROR_INVALID_ARGUMENT :
            ch_http_endpoint_prepare(next_url, "Outline key", &next, error);
        curl_free(next_url);
        if (status == CH_OK && !ch_http_endpoint_same_origin(&initial, &next)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "Outline configuration redirect changes origin");
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        if (status == CH_OK) {
            ch_http_endpoint_clear(&endpoint);
            endpoint = next;
        } else {
            ch_http_endpoint_clear(&next);
        }
    }
    if (status == CH_OK && (http_status < 200L || http_status >= 300L)) {
        ch_error_set(error, CH_ERROR_IO,
                     "fetch Outline configuration: HTTP status %ld",
                     http_status);
        status = CH_ERROR_IO;
    }
    if (status == CH_OK && body.length == 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "Outline configuration is empty");
        status = CH_ERROR_PARSE;
    }
    ch_http_endpoint_clear(&initial);
    ch_http_endpoint_clear(&endpoint);
    free(url);
    if (status != CH_OK) { free(body.data); return status; }
    *out_body = body.data;
    return CH_OK;
}

static ch_status outline_parse_resolved_body(const char *body,
                                             outline_key *key,
                                             ch_error *error) {
    char *trimmed = outline_trim_copy(body);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status;
    if (strncasecmp(trimmed, "ss://", 5U) == 0) {
        status = outline_parse_ss(trimmed, key, error);
    } else if (trimmed[0] == '{') {
        status = outline_parse_legacy_json(trimmed, key, error);
    } else if (strncasecmp(trimmed, "ssconf://", 9U) == 0) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "nested dynamic Outline keys are not supported");
        status = CH_ERROR_UNSUPPORTED;
    } else {
        status = outline_parse_yaml(trimmed, key, error);
    }
    free(trimmed);
    return status;
}

static ch_status outline_resolve(const char *raw, outline_key *key,
                                 ch_error *error) {
    ch_error_clear(error);
    memset(key, 0, sizeof(*key));
    char *trimmed = outline_trim_copy(raw);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline access key");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (strncasecmp(trimmed, "https://", 8U) == 0) {
        char *fragment = strchr(trimmed, '#');
        if (fragment != NULL) {
            char *decoded = outline_percent_decode(
                fragment + 1U, strlen(fragment + 1U), 0, error);
            if (decoded == NULL) { free(trimmed); return error->code; }
            if (strncasecmp(decoded, "ss://", 5U) == 0 ||
                strncasecmp(decoded, "ssconf://", 9U) == 0) {
                free(trimmed);
                trimmed = decoded;
            } else {
                free(decoded);
            }
        }
    }
    ch_status status;
    if (strncasecmp(trimmed, "ss://", 5U) == 0) {
        status = outline_parse_ss(trimmed, key, error);
    } else if (strncasecmp(trimmed, "ssconf://", 9U) == 0) {
        key->dynamic = 1;
        key->dynamic_key = ch_strdup(trimmed);
        char *body = NULL;
        status = key->dynamic_key == NULL ? CH_ERROR_OUT_OF_MEMORY :
            outline_fetch_dynamic(trimmed, &body, error);
        if (key->dynamic_key == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy dynamic Outline key");
        }
        if (status == CH_OK) status = outline_parse_resolved_body(body, key,
                                                                  error);
        free(body);
        const char *fragment = strchr(trimmed, '#');
        if (status == CH_OK && key->suggested_name == NULL && fragment != NULL) {
            key->suggested_name = outline_percent_decode(
                fragment + 1U, strlen(fragment + 1U), 0, error);
            if (key->suggested_name == NULL) status = error->code;
        }
        if (status == CH_OK) key->refreshed_at_ns = outline_now_ns();
    } else {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "use an Outline ss:// or ssconf:// access key");
        status = CH_ERROR_UNSUPPORTED;
    }
    free(trimmed);
    if (status != CH_OK) outline_key_clear(key);
    return status;
}

static char *outline_request_string(const char *request_json,
                                    const char *name, int required,
                                    ch_error *error) {
    ch_json_value *root = ch_json_parse(request_json == NULL ? "{}" :
                                       request_json,
                                       strlen(request_json == NULL ? "{}" :
                                              request_json), error);
    if (root == NULL) return NULL;
    const char *value = ch_json_string_value(ch_json_object_get(root, name));
    int missing = value == NULL;
    char *copy = value == NULL ? NULL : ch_strdup(value);
    ch_json_value_destroy(root);
    if (copy == NULL && required != 0) {
        ch_error_set(error, missing ? CH_ERROR_INVALID_ARGUMENT :
                     CH_ERROR_OUT_OF_MEMORY, missing ?
                     "%s is required" : "copy %s", name);
    }
    return copy;
}

static int outline_request_bool(const ch_json_value *root, const char *name,
                                int fallback) {
    const ch_json_value *value = ch_json_object_get(root, name);
    return value == NULL ? fallback : ch_json_bool_value(value,
                                                          fallback != 0);
}

static int outline_low_entropy(const outline_endpoint *endpoint) {
    ch_ss_cipher cipher;
    ch_error ignored;
    return ch_ss_cipher_from_name(endpoint->method, &cipher, &ignored) == CH_OK &&
        cipher.salt_size - endpoint->prefix_length < 8U;
}

static int outline_append_endpoint_review(ch_json_buffer *json,
                                          const outline_endpoint *endpoint) {
    return ch_json_append(json, "{\"address\":") &&
        ch_json_append_string(json, endpoint->address) &&
        ch_json_append(json, ",\"cipher\":") &&
        ch_json_append_string(json, endpoint->method) &&
        ch_json_append_format(json, ",\"prefix_length\":%zu}",
                              endpoint->prefix_length);
}

static ch_status outline_encode_review(outline_key *parsed, char **out_json,
                                       ch_error *error) {
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"kind\":") &&
        ch_json_append_string(&json, parsed->dynamic ? "dynamic" : "static") &&
        ch_json_append(&json, ",\"suggested_name\":") &&
        ch_json_append_string(&json, parsed->suggested_name == NULL ?
                              "Outline" : parsed->suggested_name) &&
        ch_json_append(&json, ",\"tcp\":") &&
        outline_append_endpoint_review(&json, &parsed->tcp) &&
        ch_json_append(&json, ",\"udp\":") &&
        outline_append_endpoint_review(&json, &parsed->udp) &&
        ch_json_append_format(&json,
            ",\"dynamic\":%s,\"low_entropy_warning\":%s}",
            parsed->dynamic ? "true" : "false",
            (outline_low_entropy(&parsed->tcp) ||
             outline_low_entropy(&parsed->udp)) ? "true" : "false");
    outline_key_clear(parsed);
    *out_json = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (*out_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode Outline review");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

ch_status ch_outline_review_request_json(const char *request_json,
                                         char **out_json,
                                         ch_error *error) {
    ch_error_clear(error);
    if (out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline review output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    char *access_key = outline_request_string(request_json, "access_key", 1,
                                              error);
    if (access_key == NULL) return error->code;
    outline_key parsed;
    ch_status status = outline_resolve(access_key, &parsed, error);
    free(access_key);
    if (status != CH_OK) {
        outline_key_clear(&parsed);
        return status;
    }
    return outline_encode_review(&parsed, out_json, error);
}

ch_status ch_outline_review_document_json(const char *document,
                                          char **out_json,
                                          ch_error *error) {
    ch_error_clear(error);
    if (document == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline document and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    outline_key parsed;
    memset(&parsed, 0, sizeof(parsed));
    ch_status status = outline_parse_resolved_body(document, &parsed, error);
    if (status != CH_OK) {
        outline_key_clear(&parsed);
        return status;
    }
    return outline_encode_review(&parsed, out_json, error);
}

static ch_status outline_mutation_json(const outline_key *parsed,
                                       const char *profile_name,
                                       int activate, int refresh,
                                       char **out_json, ch_error *error) {
    char *tcp_prefix = outline_base64_encode(parsed->tcp.prefix,
                                             parsed->tcp.prefix_length, error);
    char *udp_prefix = outline_base64_encode(parsed->udp.prefix,
                                             parsed->udp.prefix_length, error);
    if (tcp_prefix == NULL || udp_prefix == NULL) {
        free(tcp_prefix); free(udp_prefix);
        return error->code;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile_name\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append(&json, ",\"profile\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append_format(&json,
            ",\"activate\":%s,\"refresh\":%s,\"dynamic\":%s,"
            "\"refreshed_at_ns\":%" PRId64,
            activate ? "true" : "false", refresh ? "true" : "false",
            parsed->dynamic ? "true" : "false", parsed->refreshed_at_ns) &&
        ch_json_append(&json, ",\"dynamic_key\":") &&
        ch_json_append_string(&json, parsed->dynamic_key == NULL ? "" :
                              parsed->dynamic_key) &&
        ch_json_append(&json, ",\"tcp\":{\"address\":") &&
        ch_json_append_string(&json, parsed->tcp.address) &&
        ch_json_append(&json, ",\"method\":") &&
        ch_json_append_string(&json, parsed->tcp.method) &&
        ch_json_append(&json, ",\"password\":") &&
        ch_json_append_string(&json, parsed->tcp.password) &&
        ch_json_append(&json, ",\"prefix_base64\":") &&
        ch_json_append_string(&json, tcp_prefix) &&
        ch_json_append(&json, "},\"udp\":{\"address\":") &&
        ch_json_append_string(&json, parsed->udp.address) &&
        ch_json_append(&json, ",\"method\":") &&
        ch_json_append_string(&json, parsed->udp.method) &&
        ch_json_append(&json, ",\"password\":") &&
        ch_json_append_string(&json, parsed->udp.password) &&
        ch_json_append(&json, ",\"prefix_base64\":") &&
        ch_json_append_string(&json, udp_prefix) &&
        ch_json_append(&json, "}}");
    free(tcp_prefix); free(udp_prefix);
    *out_json = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (*out_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode Outline profile mutation");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

ch_status ch_outline_import_mutation_request_json(const char *request_json,
                                                  char **out_json,
                                                  ch_error *error) {
    ch_error_clear(error);
    if (out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Outline import output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    ch_json_value *root = ch_json_parse(request_json == NULL ? "{}" :
                                       request_json,
                                       strlen(request_json == NULL ? "{}" :
                                              request_json), error);
    if (root == NULL) return error->code;
    const char *access = ch_json_string_value(ch_json_object_get(root,
                                                                 "access_key"));
    const char *requested_name = ch_json_string_value(ch_json_object_get(
        root, "profile_name"));
    int activate = outline_request_bool(root, "activate", 0);
    char *access_copy = access == NULL ? NULL : ch_strdup(access);
    char *name_copy = requested_name == NULL ? NULL :
        outline_trim_copy(requested_name);
    ch_json_value_destroy(root);
    if (access_copy == NULL) {
        free(name_copy);
        ch_error_set(error, access == NULL ? CH_ERROR_INVALID_ARGUMENT :
                     CH_ERROR_OUT_OF_MEMORY, access == NULL ?
                     "access_key is required" : "copy Outline access key");
        return error->code;
    }
    outline_key parsed;
    ch_status status = outline_resolve(access_copy, &parsed, error);
    free(access_copy);
    if (status != CH_OK) {
        free(name_copy);
        outline_key_clear(&parsed);
        return status;
    }
    if (name_copy == NULL || name_copy[0] == '\0') {
        free(name_copy);
        name_copy = ch_strdup(parsed.suggested_name == NULL ||
                              parsed.suggested_name[0] == '\0' ?
                              "Outline" : parsed.suggested_name);
    }
    if (name_copy == NULL) {
        outline_key_clear(&parsed);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Outline profile name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    status = outline_mutation_json(&parsed, name_copy, activate, 0, out_json,
                                   error);
    free(name_copy);
    outline_key_clear(&parsed);
    return status;
}

ch_status ch_outline_refresh_mutation_request_json(const ch_config *config,
                                                   const char *request_json,
                                                   char **out_json,
                                                   ch_error *error) {
    ch_error_clear(error);
    if (config == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config and Outline refresh output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    char *profile_name = outline_request_string(request_json, "profile", 0,
                                                error);
    if (error->code != CH_OK) return error->code;
    const ch_config_table *profile = profile_name == NULL ||
        profile_name[0] == '\0' ? ch_config_active_profile(config) :
        ch_config_profile_named(config, profile_name);
    if (profile == NULL) {
        free(profile_name);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "Outline profile not found");
        return CH_ERROR_NOT_FOUND;
    }
    if (profile_name == NULL || profile_name[0] == '\0') {
        free(profile_name);
        profile_name = NULL;
        if (ch_config_table_get_string(profile, "name", &profile_name,
                                       error) != CH_OK) return error->code;
    }
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *server = NULL;
    char *dynamic_key = NULL;
    for (size_t chain_index = 0U; dynamic_key == NULL &&
         chain_index < ch_config_array_count(chains); ++chain_index) {
        const ch_config_array *servers = ch_config_table_get_array(
            ch_config_array_get_table(chains, chain_index), "server");
        for (size_t server_index = 0U; dynamic_key == NULL &&
             server_index < ch_config_array_count(servers); ++server_index) {
            server = ch_config_array_get_table(servers, server_index);
            const ch_config_table *settings = ch_config_table_get_table(
                server, "settings");
            ch_error ignored;
            (void)ch_config_table_get_string(settings, "outline_dynamic_key",
                                             &dynamic_key, &ignored);
            if (dynamic_key != NULL && dynamic_key[0] == '\0') {
                free(dynamic_key); dynamic_key = NULL;
            }
        }
    }
    if (dynamic_key == NULL) {
        free(profile_name);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "profile is not backed by a dynamic Outline key");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    outline_key parsed;
    ch_status status = outline_resolve(dynamic_key, &parsed, error);
    free(dynamic_key);
    if (status == CH_OK) status = outline_mutation_json(
        &parsed, profile_name, 0, 1, out_json, error);
    free(profile_name);
    outline_key_clear(&parsed);
    return status;
}
