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

typedef struct ch_ip_stack_options {
    ch_ip_stack_packet_writer packet_writer;
    void *packet_writer_context;
    const char *ipv4_address; /* Default: 198.18.0.1. */
    const char *ipv4_netmask; /* Default: 255.255.255.252. */
    const char *ipv6_address; /* Default: fd7a:636c:616d::1. */
    unsigned int mtu;         /* Default: 1500. */
} ch_ip_stack_options;

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
