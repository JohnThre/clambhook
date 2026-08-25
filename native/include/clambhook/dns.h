#ifndef CLAMBHOOK_DNS_H
#define CLAMBHOOK_DNS_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CH_DNS_MIN_MESSAGE 12U
#define CH_DNS_MAX_MESSAGE 65535U

typedef enum ch_dns_route_action {
    CH_DNS_ROUTE_CONNECT = 1,
    CH_DNS_ROUTE_DIRECT = 2,
    CH_DNS_ROUTE_BLOCK = 3,
    CH_DNS_ROUTE_REJECT = 4
} ch_dns_route_action;

/* Reports how the route planner handles one encrypted-upstream endpoint. */
typedef ch_status (*ch_dns_route_callback)(
    const char *network,
    const char *target,
    ch_dns_route_action *out_action,
    void *context,
    ch_error *error
);

/*
 * Returns a connected stream. Implementations apply routing to target and use
 * bootstrap_ips only for a direct hostname endpoint, avoiding resolver loops.
 * Ownership of the descriptor transfers to the DNS proxy on CH_OK.
 */
typedef ch_status (*ch_dns_stream_dial_callback)(
    const char *network,
    const char *target,
    const char *const *bootstrap_ips,
    size_t bootstrap_ip_count,
    int *out_descriptor,
    void *context,
    ch_error *error
);

typedef struct ch_dns_proxy_options {
    ch_dns_route_callback route;
    ch_dns_stream_dial_callback stream_dial;
    void *dial_context;
    bool insecure_skip_verify; /* Test-only injection; production leaves false. */
} ch_dns_proxy_options;

typedef struct ch_dns_proxy ch_dns_proxy;

/* Disabled DNS returns NULL with error.code == CH_OK. */
ch_dns_proxy *ch_dns_proxy_create(
    const ch_config *config,
    const char *profile_name,
    const ch_dns_proxy_options *options,
    ch_error *error
);
void ch_dns_proxy_destroy(ch_dns_proxy *proxy);

/*
 * On total upstream failure, returns the last error while also returning an
 * owned SERVFAIL response so a TUN caller can answer the client and log.
 */
ch_status ch_dns_proxy_exchange(
    ch_dns_proxy *proxy,
    const uint8_t *query,
    size_t query_length,
    uint8_t **out_response,
    size_t *out_response_length,
    ch_error *error
);

size_t ch_dns_proxy_upstream_count(const ch_dns_proxy *proxy);
const char *ch_dns_proxy_upstream_name(const ch_dns_proxy *proxy, size_t index);

/* Pure DNS wire helpers used by differential fixtures. */
size_t ch_dns_question_end(const uint8_t *message, size_t length);
ch_status ch_dns_validate_response(
    const uint8_t *query,
    size_t query_length,
    const uint8_t *response,
    size_t response_length,
    ch_error *error
);
ch_status ch_dns_servfail(
    const uint8_t *query,
    size_t query_length,
    uint8_t **out_response,
    size_t *out_response_length,
    ch_error *error
);

#ifdef __cplusplus
}
#endif

#endif
