// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/error.h"

#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "internal.h"

void ch_error_clear(ch_error *error) {
    if (error == NULL) {
        return;
    }
    error->code = CH_OK;
    error->message[0] = '\0';
}

void ch_error_set(ch_error *error, ch_status code, const char *format, ...) {
    if (error == NULL) {
        return;
    }
    error->code = code;
    if (format == NULL) {
        error->message[0] = '\0';
        return;
    }
    va_list arguments;
    va_start(arguments, format);
    (void)vsnprintf(error->message, sizeof(error->message), format, arguments);
    va_end(arguments);
}

const char *ch_status_name(ch_status status) {
    switch (status) {
        case CH_OK: return "ok";
        case CH_ERROR_INVALID_ARGUMENT: return "invalid_argument";
        case CH_ERROR_OUT_OF_MEMORY: return "out_of_memory";
        case CH_ERROR_INVALID_STATE: return "invalid_state";
        case CH_ERROR_IO: return "io";
        case CH_ERROR_PARSE: return "parse";
        case CH_ERROR_NOT_FOUND: return "not_found";
        case CH_ERROR_UNSUPPORTED: return "unsupported";
        case CH_ERROR_INTERNAL: return "internal";
    }
    return "unknown";
}

char *ch_strdup(const char *value) {
    if (value == NULL) {
        return NULL;
    }
    size_t length = strlen(value);
    if (length == SIZE_MAX) {
        return NULL;
    }
    char *copy = malloc(length + 1U);
    if (copy != NULL) {
        memcpy(copy, value, length + 1U);
    }
    return copy;
}
