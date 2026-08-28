// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_ERROR_H
#define CLAMBHOOK_ERROR_H

#ifdef __cplusplus
extern "C" {
#endif

typedef enum ch_status {
    CH_OK = 0,
    CH_ERROR_INVALID_ARGUMENT = 1,
    CH_ERROR_OUT_OF_MEMORY = 2,
    CH_ERROR_INVALID_STATE = 3,
    CH_ERROR_IO = 4,
    CH_ERROR_PARSE = 5,
    CH_ERROR_NOT_FOUND = 6,
    CH_ERROR_UNSUPPORTED = 7,
    CH_ERROR_INTERNAL = 8
} ch_status;

typedef struct ch_error {
    ch_status code;
    char message[256];
} ch_error;

void ch_error_clear(ch_error *error);
const char *ch_status_name(ch_status status);

#ifdef __cplusplus
}
#endif

#endif
