#ifndef CLAMBHOOK_INTERNAL_H
#define CLAMBHOOK_INTERNAL_H

#include <stdarg.h>
#include <stddef.h>

#include "clambhook/error.h"

void ch_error_set(ch_error *error, ch_status code, const char *format, ...)
    __attribute__((format(printf, 3, 4)));
char *ch_strdup(const char *value);

typedef struct ch_json_buffer {
    char *data;
    size_t length;
    size_t capacity;
    int failed;
} ch_json_buffer;

void ch_json_init(ch_json_buffer *buffer);
void ch_json_dispose(ch_json_buffer *buffer);
int ch_json_append(ch_json_buffer *buffer, const char *value);
int ch_json_append_format(ch_json_buffer *buffer, const char *format, ...)
    __attribute__((format(printf, 2, 3)));
int ch_json_append_string(ch_json_buffer *buffer, const char *value);
struct ch_json_value;
int ch_json_append_value(ch_json_buffer *buffer, const struct ch_json_value *value);
char *ch_json_take(ch_json_buffer *buffer);

#endif
