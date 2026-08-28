#ifndef CLAMBHOOK_LISTENER_H
#define CLAMBHOOK_LISTENER_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef enum ch_proxy_listener_protocol {
    CH_PROXY_LISTENER_SOCKS5 = 1,
    CH_PROXY_LISTENER_HTTP = 2
} ch_proxy_listener_protocol;

typedef enum ch_proxy_route_action {
    CH_PROXY_ROUTE_CONNECT = 1,
    CH_PROXY_ROUTE_BLOCK = 2,
    CH_PROXY_ROUTE_REJECT = 3
} ch_proxy_route_action;

#define CH_PROXY_ROUTE_SESSION_KEY_SIZE 384U

typedef struct ch_proxy_route {
    ch_proxy_route_action action;
    uint64_t flow_id;
    char session_key[CH_PROXY_ROUTE_SESSION_KEY_SIZE];
} ch_proxy_route;

/* Metadata-only flow callbacks. Payload contents are never exposed. */
typedef void (*ch_proxy_flow_bytes_callback)(
    uint64_t flow_id,
    uint64_t rx_delta,
    uint64_t tx_delta,
    void *context
);

typedef void (*ch_proxy_flow_close_callback)(
    uint64_t flow_id,
    const char *reason,
    void *context
);

/*
 * On CH_OK + CONNECT, callback transfers one connected stream descriptor to
 * the listener. BLOCK/REJECT must leave out_descriptor set to -1.
 */
typedef ch_status (*ch_proxy_dial_callback)(
    const char *network,
    const char *target,
    const char *source,
    ch_proxy_route *route,
    int *out_descriptor,
    void *context,
    ch_error *error
);

struct ch_packet_connection;

/* On CH_OK + CONNECT, transfers one packet connection to the listener. */
typedef ch_status (*ch_proxy_packet_dial_callback)(
    const char *network,
    const char *target,
    const char *source,
    ch_proxy_route *route,
    struct ch_packet_connection **out_connection,
    void *context,
    ch_error *error
);

typedef struct ch_proxy_listener_options {
    ch_proxy_listener_protocol protocol;
    const char *address;
    int authentication_required;
    const char *username;
    const char *password;
    size_t maximum_connections;
    unsigned int handshake_timeout_milliseconds;
    ch_proxy_dial_callback dial;
    ch_proxy_packet_dial_callback packet_dial;
    void *dial_context;
    ch_proxy_flow_bytes_callback flow_bytes;
    ch_proxy_flow_close_callback flow_close;
    void *flow_context;
} ch_proxy_listener_options;

typedef struct ch_proxy_listener ch_proxy_listener;

ch_proxy_listener *ch_proxy_listener_start(
    const ch_proxy_listener_options *options,
    ch_error *error
);

/* Idempotently stops accepting, closes active relays, joins, and frees. */
void ch_proxy_listener_stop(ch_proxy_listener *listener);

const char *ch_proxy_listener_address(const ch_proxy_listener *listener);
const char *ch_proxy_listener_protocol_name(const ch_proxy_listener *listener);
size_t ch_proxy_listener_active_connections(ch_proxy_listener *listener);

#ifdef __cplusplus
}
#endif

#endif
