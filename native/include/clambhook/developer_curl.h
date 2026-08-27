#ifndef CLAMBHOOK_DEVELOPER_CURL_H
#define CLAMBHOOK_DEVELOPER_CURL_H

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Parses {"curl":"..."} into the compose-window request shape without
 * executing the command or reading any @file argument. */
char *ch_developer_curl_import_json(const char *request_json,
                                    ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
