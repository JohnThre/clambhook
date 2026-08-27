#ifndef CLAMBHOOK_IP_STACK_H
#define CLAMBHOOK_IP_STACK_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_ip_stack ch_ip_stack;

typedef void (*ch_ip_stack_packet_writer)(
    const uint8_t *packet,
    size_t length,
    void *context
);

typedef ch_status (*ch_ip_stack_tcp_dialer)(
    const char *target,
    const char *source,
    const char *domain_hint,
    int *out_descriptor,
    uint64_t *out_flow_id,
    void *context,
    ch_error *error
);

typedef ch_status (*ch_ip_stack_udp_dialer)(
    const char *target,
    const char *source,
    const char *domain_hint,
    void **out_connection,
    uint64_t *out_flow_id,
    void *context,
    ch_error *error
);

typedef ch_status (*ch_ip_stack_udp_sender)(
    void *connection,
    const char *target,
    const uint8_t *payload,
    size_t payload_length,
    ch_error *error
);

/* CH_ERROR_NOT_FOUND means that no datagram is currently available. */
typedef ch_status (*ch_ip_stack_udp_receiver)(
    void *connection,
    uint8_t *buffer,
    size_t buffer_capacity,
    size_t *out_length,
    char **out_source,
    ch_error *error
);

typedef void (*ch_ip_stack_udp_closer)(void *connection);

/* Metadata-only lifecycle callbacks; payload bytes are never exposed. */
typedef void (*ch_ip_stack_flow_bytes_observer)(
    uint64_t flow_id,
    uint64_t rx_delta,
    uint64_t tx_delta,
    void *context
);

typedef void (*ch_ip_stack_flow_close_observer)(
    uint64_t flow_id,
    const char *reason,
    void *context
);

typedef ch_status (*ch_ip_stack_dns_exchange)(
    const uint8_t *query,
    size_t query_length,
    uint8_t **out_response,
    size_t *out_response_length,
    void *context,
    ch_error *error
);

typedef struct ch_ip_stack_options {
    ch_ip_stack_packet_writer packet_writer;
    void *packet_writer_context;
    ch_ip_stack_tcp_dialer tcp_dialer;
    void *tcp_dialer_context;
    ch_ip_stack_udp_dialer udp_dialer;
    void *udp_dialer_context;
    ch_ip_stack_udp_sender udp_sender;
    ch_ip_stack_udp_receiver udp_receiver;
    ch_ip_stack_udp_closer udp_closer;
    ch_ip_stack_flow_bytes_observer flow_bytes;
    ch_ip_stack_flow_close_observer flow_close;
    void *flow_observer_context;
    ch_ip_stack_dns_exchange dns_exchange;
    void *dns_exchange_context;
    const char *ipv4_address; /* Default: 198.18.0.1. */
    const char *ipv4_netmask; /* Default: 255.255.255.252. */
    const char *ipv6_address; /* Default: fd7a:636c:616d::1. */
    unsigned int mtu;         /* Default: 1500. */
} ch_ip_stack_options;

/*
 * A successful TCP dial transfers ownership of out_descriptor to the stack
 * and may return a non-zero metadata flow identifier through out_flow_id.
 * target and source are numeric host:port strings valid only during the call.
 * domain_hint is either an observed DNS hostname with the same target port or
 * an empty string; it is for rule matching only and must not replace target
 * when opening the transport.
 * A successful UDP dial likewise transfers the opaque connection to the stack;
 * it is released with udp_closer. UDP receive source strings are released with
 * free(). DNS responses are owned buffers and are also released with free().
 */

/*
 * The lwIP raw API is process-global and single-context. At most one stack may
 * be active, and create/inject/tick/destroy must be serialized by its owner.
 */
ch_ip_stack *ch_ip_stack_create(const ch_ip_stack_options *options,
                                ch_error *error);
void ch_ip_stack_destroy(ch_ip_stack *stack);
ch_status ch_ip_stack_inject(ch_ip_stack *stack, const uint8_t *packet,
                             size_t length, ch_error *error);
void ch_ip_stack_tick(ch_ip_stack *stack);

#ifdef __cplusplus
}
#endif

#endif
