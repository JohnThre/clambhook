#ifndef CLAMBHOOK_HTTP_SAFETY_H
#define CLAMBHOOK_HTTP_SAFETY_H

#include "clambhook/error.h"

typedef struct ch_http_endpoint {
    char *url;
    char *scheme;
    char *host;
    char *port;
    char *resolve;
} ch_http_endpoint;

void ch_http_endpoint_clear(ch_http_endpoint *endpoint);

/* Parses an HTTP(S) URL, resolves every address, rejects local/private/cloud
 * metadata targets, and produces a CURLOPT_RESOLVE entry which pins the
 * validated address for the subsequent request. */
ch_status ch_http_endpoint_prepare(const char *url, const char *purpose,
                                   ch_http_endpoint *out, ch_error *error);

int ch_http_endpoint_same_origin(const ch_http_endpoint *first,
                                 const ch_http_endpoint *next);

/* Resolves an absolute or relative Location value against base. The returned
 * URL belongs to libcurl and must be released with curl_free(). */
char *ch_http_resolve_redirect(const char *base, const char *location);

#endif
