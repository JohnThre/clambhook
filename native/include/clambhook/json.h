#ifndef CLAMBHOOK_JSON_H
#define CLAMBHOOK_JSON_H

#include <stdbool.h>
#include <stddef.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef enum ch_json_type {
    CH_JSON_NULL,
    CH_JSON_BOOL,
    CH_JSON_NUMBER,
    CH_JSON_STRING,
    CH_JSON_ARRAY,
    CH_JSON_OBJECT
} ch_json_type;

typedef struct ch_json_value ch_json_value;

ch_json_value *ch_json_parse(const char *json, size_t length, ch_error *error);
void ch_json_value_destroy(ch_json_value *value);
ch_json_type ch_json_value_type(const ch_json_value *value);

bool ch_json_bool_value(const ch_json_value *value, bool fallback);
double ch_json_number_value(const ch_json_value *value, double fallback);
const char *ch_json_string_value(const ch_json_value *value);

size_t ch_json_array_size(const ch_json_value *value);
const ch_json_value *ch_json_array_get(const ch_json_value *value, size_t index);
size_t ch_json_object_size(const ch_json_value *value);
const char *ch_json_object_key(const ch_json_value *value, size_t index);
const ch_json_value *ch_json_object_value(const ch_json_value *value, size_t index);
const ch_json_value *ch_json_object_get(const ch_json_value *value, const char *key);

#ifdef __cplusplus
}
#endif

#endif
