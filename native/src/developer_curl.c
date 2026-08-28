// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/developer_curl.h"

#include <ctype.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include "clambhook/json.h"
#include "internal.h"

#define CH_DEVELOPER_CURL_MAX_BYTES (1024U * 1024U)
#define CH_DEVELOPER_CURL_MAX_TOKENS 4096U

typedef struct developer_curl_tokens {
    char **items;
    size_t count;
} developer_curl_tokens;

typedef struct developer_curl_header {
    char *name;
    char *value;
} developer_curl_header;

static void developer_curl_tokens_clear(developer_curl_tokens *tokens) {
    if (tokens == NULL) return;
    for (size_t index = 0U; index < tokens->count; ++index) {
        free(tokens->items[index]);
    }
    free(tokens->items);
    memset(tokens, 0, sizeof(*tokens));
}

static bool developer_curl_tokens_append(developer_curl_tokens *tokens,
                                         char *token, ch_error *error) {
    if (tokens->count >= CH_DEVELOPER_CURL_MAX_TOKENS) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "cURL command has too many arguments");
        return false;
    }
    char **grown = realloc(tokens->items,
                           (tokens->count + 1U) * sizeof(*grown));
    if (grown == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "append cURL argument");
        return false;
    }
    tokens->items = grown;
    tokens->items[tokens->count++] = token;
    return true;
}

static bool developer_curl_finish_token(developer_curl_tokens *tokens,
                                        ch_json_buffer *current,
                                        bool *has_token,
                                        ch_error *error) {
    if (!*has_token) return true;
    char *token = ch_json_take(current);
    if (token == NULL ||
        !developer_curl_tokens_append(tokens, token, error)) {
        free(token);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "finish cURL argument");
        }
        return false;
    }
    *has_token = false;
    return true;
}

static bool developer_curl_tokenize(const char *text,
                                    developer_curl_tokens *tokens,
                                    ch_error *error) {
    memset(tokens, 0, sizeof(*tokens));
    size_t length = strlen(text);
    if (length > CH_DEVELOPER_CURL_MAX_BYTES) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "cURL command exceeds 1 MiB limit");
        return false;
    }
    ch_json_buffer current;
    ch_json_init(&current);
    bool single_quote = false;
    bool double_quote = false;
    bool has_token = false;
    bool okay = true;
    for (size_t index = 0U; okay && index < length; ++index) {
        char character = text[index];
        if (single_quote) {
            if (character == '\'') {
                single_quote = false;
            } else {
                okay = ch_json_append_bytes(&current, &character, 1U) != 0;
            }
            continue;
        }
        if (double_quote) {
            if (character == '"') {
                double_quote = false;
            } else if (character == '\\' && index + 1U < length) {
                char next = text[index + 1U];
                if (next == '"' || next == '\\' || next == '$' ||
                    next == '`' || next == '\n') {
                    okay = ch_json_append_bytes(&current, &next, 1U) != 0;
                    ++index;
                } else {
                    okay = ch_json_append_bytes(&current, &character, 1U) != 0;
                }
            } else {
                okay = ch_json_append_bytes(&current, &character, 1U) != 0;
            }
            continue;
        }
        if (character == '\'') {
            single_quote = true;
            has_token = true;
        } else if (character == '"') {
            double_quote = true;
            has_token = true;
        } else if (character == '\\') {
            if (index + 1U < length) {
                char next = text[++index];
                okay = ch_json_append_bytes(&current, &next, 1U) != 0;
                has_token = true;
            }
        } else if (isspace((unsigned char)character) != 0) {
            okay = developer_curl_finish_token(tokens, &current, &has_token,
                                               error);
        } else {
            okay = ch_json_append_bytes(&current, &character, 1U) != 0;
            has_token = true;
        }
    }
    if (okay && (single_quote || double_quote)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "unterminated quote in cURL command");
        okay = false;
    }
    if (okay) {
        okay = developer_curl_finish_token(tokens, &current, &has_token,
                                           error);
    }
    if (!okay && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "tokenize cURL command");
    }
    ch_json_dispose(&current);
    if (!okay) developer_curl_tokens_clear(tokens);
    return okay;
}

static char *developer_curl_trim_copy(const char *text, bool uppercase) {
    const char *start = text == NULL ? "" : text;
    while (*start != '\0' && isspace((unsigned char)*start) != 0) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - start);
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    for (size_t index = 0U; index < length; ++index) {
        copy[index] = uppercase ?
            (char)toupper((unsigned char)start[index]) : start[index];
    }
    copy[length] = '\0';
    return copy;
}

static bool developer_curl_ends_with(const char *text, const char *suffix) {
    size_t text_length = strlen(text);
    size_t suffix_length = strlen(suffix);
    return text_length >= suffix_length &&
        strcasecmp(text + text_length - suffix_length, suffix) == 0;
}

static bool developer_curl_boolean_flag(const char *token) {
    static const char *const flags[] = {
        "--compressed", "-k", "--insecure", "-L", "--location",
        "-i", "--include", "-s", "--silent", "-S", "--show-error",
        "-v", "--verbose", "-G", "--get", "-I", "--head"
    };
    for (size_t index = 0U; index < sizeof(flags) / sizeof(flags[0]); ++index) {
        if (strcmp(token, flags[index]) == 0) return true;
    }
    return false;
}

static bool developer_curl_ignored_value_flag(const char *token) {
    static const char *const flags[] = {
        "-o", "--output", "--connect-timeout", "-m", "--max-time",
        "--retry", "-u", "--user", "-b", "--cookie", "-x", "--proxy",
        "-U", "--proxy-user", "--resolve", "--dns-servers"
    };
    for (size_t index = 0U; index < sizeof(flags) / sizeof(flags[0]); ++index) {
        if (strcmp(token, flags[index]) == 0) return true;
    }
    return false;
}

static bool developer_curl_data_flag(const char *token) {
    return strcmp(token, "-d") == 0 || strcmp(token, "--data") == 0 ||
        strcmp(token, "--data-raw") == 0 ||
        strcmp(token, "--data-binary") == 0 ||
        strcmp(token, "--data-ascii") == 0;
}

static void developer_curl_headers_clear(developer_curl_header *headers,
                                         size_t count) {
    for (size_t index = 0U; index < count; ++index) {
        free(headers[index].name);
        free(headers[index].value);
    }
    free(headers);
}

static bool developer_curl_header_append(developer_curl_header **headers,
                                         size_t *count, const char *raw,
                                         ch_error *error) {
    const char *separator = strchr(raw, ':');
    size_t name_length = separator == NULL ? strlen(raw) :
        (size_t)(separator - raw);
    char *name_raw = malloc(name_length + 1U);
    if (name_raw == NULL) goto out_of_memory;
    memcpy(name_raw, raw, name_length);
    name_raw[name_length] = '\0';
    char *name = developer_curl_trim_copy(name_raw, false);
    free(name_raw);
    char *value = developer_curl_trim_copy(
        separator == NULL ? "" : separator + 1U, false);
    if (name == NULL || value == NULL) {
        free(name);
        free(value);
        goto out_of_memory;
    }
    developer_curl_header *grown = realloc(
        *headers, (*count + 1U) * sizeof(*grown));
    if (grown == NULL) {
        free(name);
        free(value);
        goto out_of_memory;
    }
    *headers = grown;
    (*headers)[*count] = (developer_curl_header){
        .name = name,
        .value = value
    };
    ++*count;
    return true;

out_of_memory:
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                 "append cURL header");
    return false;
}

static bool developer_curl_require_value(const developer_curl_tokens *tokens,
                                         size_t index, const char *flag,
                                         ch_error *error) {
    if (index + 1U < tokens->count) return true;
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                 "%s requires a value", flag);
    return false;
}

static char *developer_curl_encode(const char *method, const char *url,
                                   const developer_curl_header *headers,
                                   size_t header_count,
                                   const ch_json_buffer *body,
                                   ch_error *error) {
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = ch_json_append(&json, "{\"method\":") &&
        ch_json_append_string(&json, method) &&
        ch_json_append(&json, ",\"url\":") &&
        ch_json_append_string(&json, url) &&
        ch_json_append(&json, ",\"headers\":[");
    for (size_t index = 0U; okay && index < header_count; ++index) {
        okay = (index == 0U || ch_json_append(&json, ",")) &&
            ch_json_append(&json, "{\"name\":") &&
            ch_json_append_string(&json, headers[index].name) &&
            ch_json_append(&json, ",\"value\":") &&
            ch_json_append_string(&json, headers[index].value) &&
            ch_json_append(&json, "}");
    }
    okay = okay && ch_json_append(&json, "],\"body\":") &&
        ch_json_append_string(&json, body->data == NULL ? "" : body->data) &&
        ch_json_append(&json, "}");
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode imported cURL command");
    }
    return result;
}

char *ch_developer_curl_import_json(const char *request_json,
                                    ch_error *error) {
    ch_error_clear(error);
    const char *document = request_json == NULL ? "{}" : request_json;
    ch_json_value *root = ch_json_parse(document, strlen(document), error);
    if (root == NULL) return NULL;
    const char *text = ch_json_value_type(root) == CH_JSON_OBJECT ?
        ch_json_string_value(ch_json_object_get(root, "curl")) : NULL;
    if (text == NULL) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "cURL import requires a string curl field");
        return NULL;
    }
    char *trimmed = developer_curl_trim_copy(text, false);
    ch_json_value_destroy(root);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy cURL command");
        return NULL;
    }
    developer_curl_tokens tokens;
    if (!developer_curl_tokenize(trimmed, &tokens, error)) {
        free(trimmed);
        return NULL;
    }
    free(trimmed);

    size_t index = 0U;
    if (tokens.count > 0U &&
        (strcasecmp(tokens.items[0], "curl") == 0 ||
         developer_curl_ends_with(tokens.items[0], "/curl"))) {
        index = 1U;
    }
    char *method = ch_strdup("GET");
    char *url = NULL;
    developer_curl_header *headers = NULL;
    size_t header_count = 0U;
    ch_json_buffer body;
    ch_json_init(&body);
    size_t data_count = 0U;
    bool method_set = false;
    bool okay = method != NULL;
    for (; okay && index < tokens.count; ++index) {
        const char *token = tokens.items[index];
        if (strcmp(token, "-X") == 0 ||
            strcmp(token, "--request") == 0) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            char *next = developer_curl_trim_copy(tokens.items[++index], true);
            if (next == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy cURL method");
                okay = false;
                break;
            }
            free(method);
            method = next;
            method_set = true;
        } else if (strcmp(token, "-H") == 0 ||
                   strcmp(token, "--header") == 0) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            okay = developer_curl_header_append(
                &headers, &header_count, tokens.items[++index], error);
        } else if (developer_curl_data_flag(token)) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            const char *value = tokens.items[++index];
            if (value[0] == '@') {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "%s @file is not supported; paste the body inline",
                             token);
                okay = false;
                break;
            }
            okay = (data_count == 0U || ch_json_append(&body, "&")) &&
                ch_json_append(&body, value);
            if (okay) ++data_count;
            if (!method_set) {
                free(method);
                method = ch_strdup("POST");
                okay = okay && method != NULL;
            }
        } else if (strcmp(token, "-A") == 0 ||
                   strcmp(token, "--user-agent") == 0 ||
                   strcmp(token, "-e") == 0 ||
                   strcmp(token, "--referer") == 0) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            const char *name = (strcmp(token, "-A") == 0 ||
                                strcmp(token, "--user-agent") == 0) ?
                "User-Agent" : "Referer";
            const char *value = tokens.items[++index];
            ch_json_buffer joined;
            ch_json_init(&joined);
            bool joined_ok = ch_json_append(&joined, name) &&
                ch_json_append(&joined, ": ") && ch_json_append(&joined, value);
            char *raw = joined_ok ? ch_json_take(&joined) : NULL;
            ch_json_dispose(&joined);
            if (raw == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy cURL shorthand header");
                okay = false;
            } else {
                okay = developer_curl_header_append(
                    &headers, &header_count, raw, error);
            }
            free(raw);
        } else if (strcmp(token, "--url") == 0) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            char *next = ch_strdup(tokens.items[++index]);
            if (next == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy cURL URL");
                okay = false;
            } else {
                free(url);
                url = next;
            }
        } else if (developer_curl_ignored_value_flag(token)) {
            if (!developer_curl_require_value(&tokens, index, token, error)) {
                okay = false;
                break;
            }
            ++index;
        } else if (developer_curl_boolean_flag(token) || token[0] == '-') {
            continue;
        } else {
            char *next = ch_strdup(token);
            if (next == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy cURL URL");
                okay = false;
            } else {
                free(url);
                url = next;
            }
        }
    }
    if (okay && (url == NULL || url[0] == '\0')) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "cURL command has no URL");
        okay = false;
    }
    if (!okay && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "parse cURL command");
    }
    char *result = okay ? developer_curl_encode(
        method, url, headers, header_count, &body, error) : NULL;
    ch_json_dispose(&body);
    developer_curl_headers_clear(headers, header_count);
    free(method);
    free(url);
    developer_curl_tokens_clear(&tokens);
    return result;
}
