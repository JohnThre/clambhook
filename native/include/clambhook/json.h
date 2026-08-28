// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_JSON_H
#define CLAMBHOOK_JSON_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

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
/* Returns true only when the source token was an exact signed 64-bit integer. */
bool ch_json_int64_value(const ch_json_value *value, int64_t *out_value);
const char *ch_json_string_value(const ch_json_value *value);

size_t ch_json_array_size(const ch_json_value *value);
const ch_json_value *ch_json_array_get(const ch_json_value *value, size_t index);
size_t ch_json_object_size(const ch_json_value *value);
const char *ch_json_object_key(const ch_json_value *value, size_t index);
const ch_json_value *ch_json_object_value(const ch_json_value *value, size_t index);
const ch_json_value *ch_json_object_get(const ch_json_value *value, const char *key);

/* Mutable tree helpers used by validated native configuration transactions. */
ch_json_value *ch_json_value_clone(const ch_json_value *value);
ch_json_value *ch_json_value_new_bool(bool value);
ch_json_value *ch_json_value_new_number(double value);
ch_json_value *ch_json_value_new_int64(int64_t value);
ch_json_value *ch_json_value_new_string(const char *value);
ch_json_value *ch_json_value_new_array(void);
ch_json_value *ch_json_value_new_object(void);
ch_json_value *ch_json_array_get_mutable(ch_json_value *value, size_t index);
ch_json_value *ch_json_object_get_mutable(ch_json_value *value,
                                          const char *key);
/* On success array owns item; on failure ownership stays with caller. */
ch_status ch_json_array_append(ch_json_value *array, ch_json_value *item,
                               ch_error *error);
/* On success object owns member_value; on failure ownership stays with caller. */
ch_status ch_json_object_set(ch_json_value *object, const char *key,
                             ch_json_value *member_value, ch_error *error);
bool ch_json_object_remove(ch_json_value *object, const char *key);

#ifdef __cplusplus
}
#endif

#endif
