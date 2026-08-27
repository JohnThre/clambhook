#include "internal.h"

#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/json.h"

static int ch_json_reserve(ch_json_buffer *buffer, size_t extra) {
    if (buffer == NULL || buffer->failed) {
        return 0;
    }
    if (extra > SIZE_MAX - buffer->length - 1U) {
        buffer->failed = 1;
        return 0;
    }
    size_t needed = buffer->length + extra + 1U;
    if (needed <= buffer->capacity) {
        return 1;
    }
    size_t next = buffer->capacity == 0U ? 128U : buffer->capacity;
    while (next < needed) {
        if (next > SIZE_MAX / 2U) {
            next = needed;
            break;
        }
        next *= 2U;
    }
    char *grown = realloc(buffer->data, next);
    if (grown == NULL) {
        buffer->failed = 1;
        return 0;
    }
    buffer->data = grown;
    buffer->capacity = next;
    return 1;
}

void ch_json_init(ch_json_buffer *buffer) {
    if (buffer != NULL) {
        memset(buffer, 0, sizeof(*buffer));
    }
}

void ch_json_dispose(ch_json_buffer *buffer) {
    if (buffer != NULL) {
        free(buffer->data);
        memset(buffer, 0, sizeof(*buffer));
    }
}

int ch_json_append(ch_json_buffer *buffer, const char *value) {
    if (buffer == NULL || value == NULL) {
        return 0;
    }
    size_t length = strlen(value);
    if (!ch_json_reserve(buffer, length)) {
        return 0;
    }
    memcpy(buffer->data + buffer->length, value, length);
    buffer->length += length;
    buffer->data[buffer->length] = '\0';
    return 1;
}

int ch_json_append_bytes(ch_json_buffer *buffer, const char *value,
                         size_t length) {
    if (buffer == NULL || (value == NULL && length != 0U) ||
        !ch_json_reserve(buffer, length)) {
        return 0;
    }
    if (length > 0U) {
        memcpy(buffer->data + buffer->length, value, length);
    }
    buffer->length += length;
    buffer->data[buffer->length] = '\0';
    return 1;
}

int ch_json_append_format(ch_json_buffer *buffer, const char *format, ...) {
    if (buffer == NULL || format == NULL) {
        return 0;
    }
    va_list arguments;
    va_start(arguments, format);
    va_list copy;
    va_copy(copy, arguments);
    int count = vsnprintf(NULL, 0, format, copy);
    va_end(copy);
    if (count < 0 || !ch_json_reserve(buffer, (size_t)count)) {
        va_end(arguments);
        return 0;
    }
    (void)vsnprintf(buffer->data + buffer->length, (size_t)count + 1U, format, arguments);
    va_end(arguments);
    buffer->length += (size_t)count;
    return 1;
}

int ch_json_append_string(ch_json_buffer *buffer, const char *value) {
    static const char hex[] = "0123456789abcdef";
    if (!ch_json_append(buffer, "\"")) {
        return 0;
    }
    const unsigned char *cursor = (const unsigned char *)(value == NULL ? "" : value);
    for (; *cursor != 0U; ++cursor) {
        switch (*cursor) {
            case '"': if (!ch_json_append(buffer, "\\\"")) return 0; break;
            case '\\': if (!ch_json_append(buffer, "\\\\")) return 0; break;
            case '\b': if (!ch_json_append(buffer, "\\b")) return 0; break;
            case '\f': if (!ch_json_append(buffer, "\\f")) return 0; break;
            case '\n': if (!ch_json_append(buffer, "\\n")) return 0; break;
            case '\r': if (!ch_json_append(buffer, "\\r")) return 0; break;
            case '\t': if (!ch_json_append(buffer, "\\t")) return 0; break;
            default:
                if (*cursor < 0x20U) {
                    char escaped[7] = {'\\', 'u', '0', '0', hex[*cursor >> 4U], hex[*cursor & 0x0fU], '\0'};
                    if (!ch_json_append(buffer, escaped)) return 0;
                } else {
                    char character[2] = {(char)*cursor, '\0'};
                    if (!ch_json_append(buffer, character)) return 0;
                }
        }
    }
    return ch_json_append(buffer, "\"");
}

char *ch_json_take(ch_json_buffer *buffer) {
    if (buffer == NULL || buffer->failed) {
        ch_json_dispose(buffer);
        return NULL;
    }
    if (buffer->data == NULL) {
        buffer->data = ch_strdup("");
        if (buffer->data == NULL) {
            return NULL;
        }
    }
    char *result = buffer->data;
    buffer->data = NULL;
    buffer->length = 0U;
    buffer->capacity = 0U;
    return result;
}

typedef struct ch_json_member {
    char *key;
    ch_json_value *value;
} ch_json_member;

struct ch_json_value {
    ch_json_type type;
    union {
        bool boolean;
        struct {
            double value;
            int64_t integer;
            bool is_integer;
        } number;
        char *string;
        struct {
            ch_json_value **items;
            size_t count;
        } array;
        struct {
            ch_json_member *members;
            size_t count;
        } object;
    } as;
};

typedef struct ch_json_parser {
    const char *start;
    const char *cursor;
    const char *end;
    unsigned depth;
    ch_error *error;
} ch_json_parser;

static void ch_json_parse_error(ch_json_parser *parser, const char *message) {
    ch_error_set(
        parser->error,
        CH_ERROR_PARSE,
        "JSON parse error at byte %zu: %s",
        (size_t)(parser->cursor - parser->start),
        message
    );
}

static void ch_json_skip_space(ch_json_parser *parser) {
    while (parser->cursor < parser->end && isspace((unsigned char)*parser->cursor)) {
        ++parser->cursor;
    }
}

static ch_json_value *ch_json_new_value(ch_json_parser *parser, ch_json_type type) {
    ch_json_value *value = calloc(1U, sizeof(*value));
    if (value == NULL) {
        ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "allocate JSON value");
        return NULL;
    }
    value->type = type;
    return value;
}

void ch_json_value_destroy(ch_json_value *value) {
    if (value == NULL) return;
    if (value->type == CH_JSON_STRING) {
        free(value->as.string);
    } else if (value->type == CH_JSON_ARRAY) {
        for (size_t index = 0U; index < value->as.array.count; ++index) {
            ch_json_value_destroy(value->as.array.items[index]);
        }
        free(value->as.array.items);
    } else if (value->type == CH_JSON_OBJECT) {
        for (size_t index = 0U; index < value->as.object.count; ++index) {
            free(value->as.object.members[index].key);
            ch_json_value_destroy(value->as.object.members[index].value);
        }
        free(value->as.object.members);
    }
    free(value);
}

static int ch_json_hex(char character) {
    if (character >= '0' && character <= '9') return character - '0';
    if (character >= 'a' && character <= 'f') return character - 'a' + 10;
    if (character >= 'A' && character <= 'F') return character - 'A' + 10;
    return -1;
}

static int ch_json_code_unit(ch_json_parser *parser, uint32_t *code_unit) {
    if ((size_t)(parser->end - parser->cursor) < 4U) {
        ch_json_parse_error(parser, "incomplete Unicode escape");
        return 0;
    }
    uint32_t value = 0U;
    for (unsigned index = 0U; index < 4U; ++index) {
        int digit = ch_json_hex(parser->cursor[index]);
        if (digit < 0) {
            ch_json_parse_error(parser, "invalid Unicode escape");
            return 0;
        }
        value = (value << 4U) | (uint32_t)digit;
    }
    parser->cursor += 4;
    *code_unit = value;
    return 1;
}

static int ch_json_append_codepoint(ch_json_buffer *buffer, uint32_t codepoint) {
    char encoded[5] = {0};
    if (codepoint <= 0x7fU) {
        encoded[0] = (char)codepoint;
    } else if (codepoint <= 0x7ffU) {
        encoded[0] = (char)(0xc0U | (codepoint >> 6U));
        encoded[1] = (char)(0x80U | (codepoint & 0x3fU));
    } else if (codepoint <= 0xffffU) {
        encoded[0] = (char)(0xe0U | (codepoint >> 12U));
        encoded[1] = (char)(0x80U | ((codepoint >> 6U) & 0x3fU));
        encoded[2] = (char)(0x80U | (codepoint & 0x3fU));
    } else if (codepoint <= 0x10ffffU) {
        encoded[0] = (char)(0xf0U | (codepoint >> 18U));
        encoded[1] = (char)(0x80U | ((codepoint >> 12U) & 0x3fU));
        encoded[2] = (char)(0x80U | ((codepoint >> 6U) & 0x3fU));
        encoded[3] = (char)(0x80U | (codepoint & 0x3fU));
    } else {
        return 0;
    }
    return ch_json_append(buffer, encoded);
}

static char *ch_json_parse_string_raw(ch_json_parser *parser) {
    if (parser->cursor >= parser->end || *parser->cursor != '"') {
        ch_json_parse_error(parser, "expected string");
        return NULL;
    }
    ++parser->cursor;
    ch_json_buffer output;
    ch_json_init(&output);
    while (parser->cursor < parser->end) {
        unsigned char character = (unsigned char)*parser->cursor++;
        if (character == '"') {
            char *result = ch_json_take(&output);
            if (result == NULL) ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "allocate JSON string");
            return result;
        }
        if (character < 0x20U) {
            ch_json_dispose(&output);
            ch_json_parse_error(parser, "unescaped control character");
            return NULL;
        }
        if (character != '\\') {
            char bytes[2] = {(char)character, '\0'};
            if (!ch_json_append(&output, bytes)) goto allocation_failure;
            continue;
        }
        if (parser->cursor >= parser->end) {
            ch_json_dispose(&output);
            ch_json_parse_error(parser, "incomplete escape");
            return NULL;
        }
        char escape = *parser->cursor++;
        const char *replacement = NULL;
        switch (escape) {
            case '"': replacement = "\""; break;
            case '\\': replacement = "\\"; break;
            case '/': replacement = "/"; break;
            case 'b': replacement = "\b"; break;
            case 'f': replacement = "\f"; break;
            case 'n': replacement = "\n"; break;
            case 'r': replacement = "\r"; break;
            case 't': replacement = "\t"; break;
            case 'u': {
                uint32_t codepoint = 0U;
                if (!ch_json_code_unit(parser, &codepoint)) {
                    ch_json_dispose(&output);
                    return NULL;
                }
                if (codepoint >= 0xd800U && codepoint <= 0xdbffU) {
                    if ((size_t)(parser->end - parser->cursor) < 2U ||
                        parser->cursor[0] != '\\' || parser->cursor[1] != 'u') {
                        ch_json_dispose(&output);
                        ch_json_parse_error(parser, "high surrogate without low surrogate");
                        return NULL;
                    }
                    parser->cursor += 2;
                    uint32_t low = 0U;
                    if (!ch_json_code_unit(parser, &low) || low < 0xdc00U || low > 0xdfffU) {
                        ch_json_dispose(&output);
                        if (parser->error == NULL || parser->error->code == CH_OK) {
                            ch_json_parse_error(parser, "invalid low surrogate");
                        }
                        return NULL;
                    }
                    codepoint = 0x10000U + ((codepoint - 0xd800U) << 10U) + (low - 0xdc00U);
                } else if (codepoint >= 0xdc00U && codepoint <= 0xdfffU) {
                    ch_json_dispose(&output);
                    ch_json_parse_error(parser, "unexpected low surrogate");
                    return NULL;
                }
                if (!ch_json_append_codepoint(&output, codepoint)) goto allocation_failure;
                continue;
            }
            default:
                ch_json_dispose(&output);
                ch_json_parse_error(parser, "invalid escape");
                return NULL;
        }
        if (!ch_json_append(&output, replacement)) goto allocation_failure;
    }
    ch_json_dispose(&output);
    ch_json_parse_error(parser, "unterminated string");
    return NULL;

allocation_failure:
    ch_json_dispose(&output);
    ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "allocate JSON string");
    return NULL;
}

static ch_json_value *ch_json_parse_value(ch_json_parser *parser);

static ch_json_value *ch_json_parse_array(ch_json_parser *parser) {
    ch_json_value *array = ch_json_new_value(parser, CH_JSON_ARRAY);
    if (array == NULL) return NULL;
    ++parser->cursor;
    ch_json_skip_space(parser);
    if (parser->cursor < parser->end && *parser->cursor == ']') {
        ++parser->cursor;
        return array;
    }
    for (;;) {
        ch_json_value *item = ch_json_parse_value(parser);
        if (item == NULL) {
            ch_json_value_destroy(array);
            return NULL;
        }
        if (array->as.array.count == SIZE_MAX / sizeof(*array->as.array.items)) {
            ch_json_value_destroy(item);
            ch_json_value_destroy(array);
            ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "JSON array is too large");
            return NULL;
        }
        ch_json_value **grown = realloc(
            array->as.array.items,
            (array->as.array.count + 1U) * sizeof(*array->as.array.items)
        );
        if (grown == NULL) {
            ch_json_value_destroy(item);
            ch_json_value_destroy(array);
            ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "grow JSON array");
            return NULL;
        }
        array->as.array.items = grown;
        array->as.array.items[array->as.array.count++] = item;
        ch_json_skip_space(parser);
        if (parser->cursor >= parser->end) break;
        if (*parser->cursor == ']') {
            ++parser->cursor;
            return array;
        }
        if (*parser->cursor++ != ',') break;
        ch_json_skip_space(parser);
    }
    ch_json_value_destroy(array);
    ch_json_parse_error(parser, "expected comma or closing bracket");
    return NULL;
}

static ch_json_value *ch_json_parse_object(ch_json_parser *parser) {
    ch_json_value *object = ch_json_new_value(parser, CH_JSON_OBJECT);
    if (object == NULL) return NULL;
    ++parser->cursor;
    ch_json_skip_space(parser);
    if (parser->cursor < parser->end && *parser->cursor == '}') {
        ++parser->cursor;
        return object;
    }
    for (;;) {
        char *key = ch_json_parse_string_raw(parser);
        if (key == NULL) { ch_json_value_destroy(object); return NULL; }
        ch_json_skip_space(parser);
        if (parser->cursor >= parser->end || *parser->cursor++ != ':') {
            free(key); ch_json_value_destroy(object); ch_json_parse_error(parser, "expected colon"); return NULL;
        }
        ch_json_value *member_value = ch_json_parse_value(parser);
        if (member_value == NULL) { free(key); ch_json_value_destroy(object); return NULL; }
        if (object->as.object.count == SIZE_MAX / sizeof(*object->as.object.members)) {
            free(key); ch_json_value_destroy(member_value); ch_json_value_destroy(object);
            ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "JSON object is too large"); return NULL;
        }
        ch_json_member *grown = realloc(
            object->as.object.members,
            (object->as.object.count + 1U) * sizeof(*object->as.object.members)
        );
        if (grown == NULL) {
            free(key); ch_json_value_destroy(member_value); ch_json_value_destroy(object);
            ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "grow JSON object"); return NULL;
        }
        object->as.object.members = grown;
        object->as.object.members[object->as.object.count++] = (ch_json_member){key, member_value};
        ch_json_skip_space(parser);
        if (parser->cursor < parser->end && *parser->cursor == '}') {
            ++parser->cursor;
            return object;
        }
        if (parser->cursor >= parser->end || *parser->cursor++ != ',') {
            ch_json_value_destroy(object); ch_json_parse_error(parser, "expected comma or closing brace"); return NULL;
        }
        ch_json_skip_space(parser);
    }
}

static ch_json_value *ch_json_parse_number(ch_json_parser *parser) {
    const char *start = parser->cursor;
    int integer_token = 1;
    char *text = NULL;
    if (*parser->cursor == '-') ++parser->cursor;
    if (parser->cursor >= parser->end) goto invalid;
    if (*parser->cursor == '0') {
        ++parser->cursor;
    } else if (*parser->cursor >= '1' && *parser->cursor <= '9') {
        while (parser->cursor < parser->end && isdigit((unsigned char)*parser->cursor)) ++parser->cursor;
    } else goto invalid;
    if (parser->cursor < parser->end && *parser->cursor == '.') {
        integer_token = 0;
        ++parser->cursor;
        if (parser->cursor >= parser->end || !isdigit((unsigned char)*parser->cursor)) goto invalid;
        while (parser->cursor < parser->end && isdigit((unsigned char)*parser->cursor)) ++parser->cursor;
    }
    if (parser->cursor < parser->end && (*parser->cursor == 'e' || *parser->cursor == 'E')) {
        integer_token = 0;
        ++parser->cursor;
        if (parser->cursor < parser->end && (*parser->cursor == '+' || *parser->cursor == '-')) ++parser->cursor;
        if (parser->cursor >= parser->end || !isdigit((unsigned char)*parser->cursor)) goto invalid;
        while (parser->cursor < parser->end && isdigit((unsigned char)*parser->cursor)) ++parser->cursor;
    }
    size_t length = (size_t)(parser->cursor - start);
    text = strndup(start, length);
    if (text == NULL) { ch_error_set(parser->error, CH_ERROR_OUT_OF_MEMORY, "copy JSON number"); return NULL; }
    errno = 0;
    char *number_end = NULL;
    double number = strtod(text, &number_end);
    int valid = errno == 0 && number_end != text && *number_end == '\0' && isfinite(number);
    if (!valid) goto invalid;
    int64_t integer = 0;
    int exact_integer = 0;
    if (integer_token) {
        errno = 0;
        char *integer_end = NULL;
        intmax_t parsed = strtoimax(text, &integer_end, 10);
        exact_integer = errno == 0 && integer_end != text &&
            *integer_end == '\0' && parsed >= INT64_MIN && parsed <= INT64_MAX;
        if (exact_integer) integer = (int64_t)parsed;
    }
    free(text);
    ch_json_value *value = ch_json_new_value(parser, CH_JSON_NUMBER);
    if (value != NULL) {
        value->as.number.value = number;
        value->as.number.integer = integer;
        value->as.number.is_integer = exact_integer != 0;
    }
    return value;

invalid:
    free(text);
    ch_json_parse_error(parser, "invalid number");
    return NULL;
}

static ch_json_value *ch_json_parse_value(ch_json_parser *parser) {
    ch_json_skip_space(parser);
    if (++parser->depth > 64U) {
        --parser->depth;
        ch_json_parse_error(parser, "nesting exceeds 64 levels");
        return NULL;
    }
    ch_json_value *value = NULL;
    if (parser->cursor >= parser->end) {
        ch_json_parse_error(parser, "expected value");
    } else if (*parser->cursor == '"') {
        char *string = ch_json_parse_string_raw(parser);
        if (string != NULL) {
            value = ch_json_new_value(parser, CH_JSON_STRING);
            if (value == NULL) free(string); else value->as.string = string;
        }
    } else if (*parser->cursor == '[') {
        value = ch_json_parse_array(parser);
    } else if (*parser->cursor == '{') {
        value = ch_json_parse_object(parser);
    } else if ((size_t)(parser->end - parser->cursor) >= 4U && strncmp(parser->cursor, "null", 4U) == 0) {
        parser->cursor += 4; value = ch_json_new_value(parser, CH_JSON_NULL);
    } else if ((size_t)(parser->end - parser->cursor) >= 4U && strncmp(parser->cursor, "true", 4U) == 0) {
        parser->cursor += 4; value = ch_json_new_value(parser, CH_JSON_BOOL);
        if (value != NULL) value->as.boolean = true;
    } else if ((size_t)(parser->end - parser->cursor) >= 5U && strncmp(parser->cursor, "false", 5U) == 0) {
        parser->cursor += 5; value = ch_json_new_value(parser, CH_JSON_BOOL);
        if (value != NULL) value->as.boolean = false;
    } else if (*parser->cursor == '-' || isdigit((unsigned char)*parser->cursor)) {
        value = ch_json_parse_number(parser);
    } else {
        ch_json_parse_error(parser, "unexpected token");
    }
    --parser->depth;
    return value;
}

ch_json_value *ch_json_parse(const char *json, size_t length, ch_error *error) {
    ch_error_clear(error);
    if (json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "JSON input is required");
        return NULL;
    }
    ch_json_parser parser = {json, json, json + length, 0U, error};
    ch_json_value *value = ch_json_parse_value(&parser);
    if (value == NULL) return NULL;
    ch_json_skip_space(&parser);
    if (parser.cursor != parser.end) {
        ch_json_value_destroy(value);
        ch_json_parse_error(&parser, "trailing data");
        return NULL;
    }
    return value;
}

ch_json_type ch_json_value_type(const ch_json_value *value) {
    return value == NULL ? CH_JSON_NULL : value->type;
}

bool ch_json_bool_value(const ch_json_value *value, bool fallback) {
    return value != NULL && value->type == CH_JSON_BOOL ? value->as.boolean : fallback;
}

double ch_json_number_value(const ch_json_value *value, double fallback) {
    return value != NULL && value->type == CH_JSON_NUMBER ?
        value->as.number.value : fallback;
}

bool ch_json_int64_value(const ch_json_value *value, int64_t *out_value) {
    if (value == NULL || value->type != CH_JSON_NUMBER ||
        !value->as.number.is_integer || out_value == NULL) return false;
    *out_value = value->as.number.integer;
    return true;
}

const char *ch_json_string_value(const ch_json_value *value) {
    return value != NULL && value->type == CH_JSON_STRING ? value->as.string : NULL;
}

size_t ch_json_array_size(const ch_json_value *value) {
    return value != NULL && value->type == CH_JSON_ARRAY ? value->as.array.count : 0U;
}

const ch_json_value *ch_json_array_get(const ch_json_value *value, size_t index) {
    return value != NULL && value->type == CH_JSON_ARRAY && index < value->as.array.count
        ? value->as.array.items[index] : NULL;
}

size_t ch_json_object_size(const ch_json_value *value) {
    return value != NULL && value->type == CH_JSON_OBJECT ? value->as.object.count : 0U;
}

const char *ch_json_object_key(const ch_json_value *value, size_t index) {
    return value != NULL && value->type == CH_JSON_OBJECT && index < value->as.object.count
        ? value->as.object.members[index].key : NULL;
}

const ch_json_value *ch_json_object_value(const ch_json_value *value, size_t index) {
    return value != NULL && value->type == CH_JSON_OBJECT && index < value->as.object.count
        ? value->as.object.members[index].value : NULL;
}

const ch_json_value *ch_json_object_get(const ch_json_value *value, const char *key) {
    if (value == NULL || value->type != CH_JSON_OBJECT || key == NULL) return NULL;
    for (size_t index = 0U; index < value->as.object.count; ++index) {
        if (strcmp(value->as.object.members[index].key, key) == 0) {
            return value->as.object.members[index].value;
        }
    }
    return NULL;
}

ch_json_value *ch_json_value_new_bool(bool value) {
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result != NULL) {
        result->type = CH_JSON_BOOL;
        result->as.boolean = value;
    }
    return result;
}

ch_json_value *ch_json_value_new_number(double value) {
    if (!isfinite(value)) return NULL;
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result != NULL) {
        result->type = CH_JSON_NUMBER;
        result->as.number.value = value;
    }
    return result;
}

ch_json_value *ch_json_value_new_int64(int64_t value) {
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result != NULL) {
        result->type = CH_JSON_NUMBER;
        result->as.number.value = (double)value;
        result->as.number.integer = value;
        result->as.number.is_integer = true;
    }
    return result;
}

ch_json_value *ch_json_value_new_string(const char *value) {
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result == NULL) return NULL;
    result->type = CH_JSON_STRING;
    result->as.string = ch_strdup(value == NULL ? "" : value);
    if (result->as.string == NULL) {
        free(result);
        return NULL;
    }
    return result;
}

ch_json_value *ch_json_value_new_array(void) {
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result != NULL) result->type = CH_JSON_ARRAY;
    return result;
}

ch_json_value *ch_json_value_new_object(void) {
    ch_json_value *result = calloc(1U, sizeof(*result));
    if (result != NULL) result->type = CH_JSON_OBJECT;
    return result;
}

ch_json_value *ch_json_value_clone(const ch_json_value *value) {
    if (value == NULL) return NULL;
    ch_json_value *copy = calloc(1U, sizeof(*copy));
    if (copy == NULL) return NULL;
    copy->type = value->type;
    switch (value->type) {
        case CH_JSON_NULL:
            break;
        case CH_JSON_BOOL:
            copy->as.boolean = value->as.boolean;
            break;
        case CH_JSON_NUMBER:
            copy->as.number = value->as.number;
            break;
        case CH_JSON_STRING:
            copy->as.string = ch_strdup(value->as.string);
            if (copy->as.string == NULL) goto failure;
            break;
        case CH_JSON_ARRAY:
            if (value->as.array.count > 0U) {
                copy->as.array.items = calloc(
                    value->as.array.count, sizeof(*copy->as.array.items));
                if (copy->as.array.items == NULL) goto failure;
            }
            for (size_t index = 0U; index < value->as.array.count; ++index) {
                copy->as.array.items[index] = ch_json_value_clone(
                    value->as.array.items[index]);
                if (copy->as.array.items[index] == NULL) goto failure;
                ++copy->as.array.count;
            }
            break;
        case CH_JSON_OBJECT:
            if (value->as.object.count > 0U) {
                copy->as.object.members = calloc(
                    value->as.object.count, sizeof(*copy->as.object.members));
                if (copy->as.object.members == NULL) goto failure;
            }
            for (size_t index = 0U; index < value->as.object.count; ++index) {
                ch_json_member *member = &copy->as.object.members[index];
                member->key = ch_strdup(value->as.object.members[index].key);
                member->value = ch_json_value_clone(
                    value->as.object.members[index].value);
                if (member->key == NULL || member->value == NULL) {
                    free(member->key);
                    ch_json_value_destroy(member->value);
                    member->key = NULL;
                    member->value = NULL;
                    goto failure;
                }
                ++copy->as.object.count;
            }
            break;
    }
    return copy;

failure:
    ch_json_value_destroy(copy);
    return NULL;
}

ch_json_value *ch_json_array_get_mutable(ch_json_value *value, size_t index) {
    return value != NULL && value->type == CH_JSON_ARRAY &&
        index < value->as.array.count ? value->as.array.items[index] : NULL;
}

ch_json_value *ch_json_object_get_mutable(ch_json_value *value,
                                          const char *key) {
    if (value == NULL || value->type != CH_JSON_OBJECT || key == NULL) {
        return NULL;
    }
    for (size_t index = 0U; index < value->as.object.count; ++index) {
        if (strcmp(value->as.object.members[index].key, key) == 0) {
            return value->as.object.members[index].value;
        }
    }
    return NULL;
}

ch_status ch_json_array_append(ch_json_value *array, ch_json_value *item,
                               ch_error *error) {
    ch_error_clear(error);
    if (array == NULL || array->type != CH_JSON_ARRAY || item == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "JSON array and item are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (array->as.array.count == SIZE_MAX / sizeof(*array->as.array.items)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "JSON array is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_json_value **grown = realloc(
        array->as.array.items,
        (array->as.array.count + 1U) * sizeof(*grown));
    if (grown == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "grow JSON array");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    array->as.array.items = grown;
    array->as.array.items[array->as.array.count++] = item;
    return CH_OK;
}

ch_status ch_json_object_set(ch_json_value *object, const char *key,
                             ch_json_value *member_value, ch_error *error) {
    ch_error_clear(error);
    if (object == NULL || object->type != CH_JSON_OBJECT || key == NULL ||
        key[0] == '\0' || member_value == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "JSON object, key, and value are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    for (size_t index = 0U; index < object->as.object.count; ++index) {
        if (strcmp(object->as.object.members[index].key, key) == 0) {
            ch_json_value_destroy(object->as.object.members[index].value);
            object->as.object.members[index].value = member_value;
            return CH_OK;
        }
    }
    if (object->as.object.count == SIZE_MAX / sizeof(ch_json_member)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "JSON object is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *owned_key = ch_strdup(key);
    if (owned_key == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy JSON object key");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_json_member *grown = realloc(
        object->as.object.members,
        (object->as.object.count + 1U) * sizeof(*grown));
    if (grown == NULL) {
        free(owned_key);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "grow JSON object");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    object->as.object.members = grown;
    object->as.object.members[object->as.object.count++] =
        (ch_json_member){owned_key, member_value};
    return CH_OK;
}

bool ch_json_object_remove(ch_json_value *object, const char *key) {
    if (object == NULL || object->type != CH_JSON_OBJECT || key == NULL) {
        return false;
    }
    for (size_t index = 0U; index < object->as.object.count; ++index) {
        if (strcmp(object->as.object.members[index].key, key) != 0) continue;
        free(object->as.object.members[index].key);
        ch_json_value_destroy(object->as.object.members[index].value);
        if (index + 1U < object->as.object.count) {
            memmove(&object->as.object.members[index],
                    &object->as.object.members[index + 1U],
                    (object->as.object.count - index - 1U) *
                        sizeof(*object->as.object.members));
        }
        --object->as.object.count;
        return true;
    }
    return false;
}

int ch_json_append_value(ch_json_buffer *buffer, const ch_json_value *value) {
    if (buffer == NULL || value == NULL) return 0;
    switch (ch_json_value_type(value)) {
        case CH_JSON_NULL:
            return ch_json_append(buffer, "null");
        case CH_JSON_BOOL:
            return ch_json_append(buffer, ch_json_bool_value(value, false) ? "true" : "false");
        case CH_JSON_NUMBER:
            if (value->as.number.is_integer) {
                return ch_json_append_format(buffer, "%" PRId64,
                                             value->as.number.integer);
            }
            return ch_json_append_format(buffer, "%.17g",
                                         value->as.number.value);
        case CH_JSON_STRING:
            return ch_json_append_string(buffer, ch_json_string_value(value));
        case CH_JSON_ARRAY:
            if (!ch_json_append(buffer, "[")) return 0;
            for (size_t index = 0U; index < ch_json_array_size(value); ++index) {
                if ((index > 0U && !ch_json_append(buffer, ",")) ||
                    !ch_json_append_value(buffer, ch_json_array_get(value, index))) return 0;
            }
            return ch_json_append(buffer, "]");
        case CH_JSON_OBJECT:
            if (!ch_json_append(buffer, "{")) return 0;
            for (size_t index = 0U; index < ch_json_object_size(value); ++index) {
                if ((index > 0U && !ch_json_append(buffer, ",")) ||
                    !ch_json_append_string(buffer, ch_json_object_key(value, index)) ||
                    !ch_json_append(buffer, ":") ||
                    !ch_json_append_value(buffer, ch_json_object_value(value, index))) return 0;
            }
            return ch_json_append(buffer, "}");
    }
    return 0;
}
