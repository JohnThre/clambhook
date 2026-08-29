// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_DEVELOPER_INTERNAL_H
#define CLAMBHOOK_DEVELOPER_INTERNAL_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include <openssl/ssl.h>

#include "clambhook/developer.h"

typedef struct ch_developer_http_header {
    char *name;
    char *value;
} ch_developer_http_header;

typedef struct ch_developer_http_message {
    char *method;
    char *url;
    char *host;
    char *path;
    int status;
    ch_developer_http_header *headers;
    size_t header_count;
    uint8_t *body;
    size_t body_length;
    bool body_set;
} ch_developer_http_message;

typedef struct ch_developer_http_result {
    bool matched;
    bool drop;
    bool local_response;
    char *rule_id;
    char *rule_name;
    char *kind;
    char *remote_url;
    ch_developer_http_message message;
} ch_developer_http_result;

void ch_developer_http_message_clear(ch_developer_http_message *message);
void ch_developer_http_result_clear(ch_developer_http_result *result);

/* Applies request rewrites, interactive breakpoints, map rules, and no-cache
 * behavior in the same order as the frozen control implementation. */
ch_status ch_developer_process_request(
    ch_developer_manager *manager,
    const ch_developer_http_message *request,
    ch_developer_http_result *result,
    ch_error *error
);

/* Applies response rewrites, interactive breakpoints, and no-cache behavior. */
ch_status ch_developer_process_response(
    ch_developer_manager *manager,
    const ch_developer_http_message *request,
    const ch_developer_http_message *response,
    ch_developer_http_result *result,
    ch_error *error
);

bool ch_developer_should_mitm(ch_developer_manager *manager,
                              const char *host);

/* Creates a caller-owned TLS server context containing a fresh short-lived
 * leaf certificate signed by the persisted developer CA. */
ch_status ch_developer_tls_server_context(ch_developer_manager *manager,
                                          const char *host,
                                          SSL_CTX **out_context,
                                          ch_error *error);

char *ch_developer_ca_pem(ch_developer_manager *manager,
                          size_t *out_length,
                          ch_error *error);
char *ch_developer_regenerate_ca_json(ch_developer_manager *manager,
                                      ch_error *error);
char *ch_developer_pending_breakpoints_json(ch_developer_manager *manager,
                                            ch_error *error);
char *ch_developer_resolve_breakpoint_json(ch_developer_manager *manager,
                                            const char *identifier,
                                            const char *request_json,
                                            ch_error *error);

#endif
