// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_TUNNEL_STACK_H
#define CLAMBHOOK_TUNNEL_STACK_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

typedef struct ch_tunnel_stack ch_tunnel_stack;
typedef struct ch_tunnel_packet ch_tunnel_packet;

typedef void (*ch_tunnel_stack_packet_writer)(
    const uint8_t *packet, size_t length, void *context);

typedef struct ch_tunnel_stack_options {
    const char *const *addresses;
    size_t address_count;
    const char *const *dns_servers;
    size_t dns_server_count;
    unsigned int mtu;
    ch_tunnel_stack_packet_writer packet_writer;
    void *packet_writer_context;
} ch_tunnel_stack_options;

/*
 * A tunnel stack translates raw IP packets carried by a VPN protocol into
 * ordinary stream/datagram handles. It owns a dedicated lwIP netif while all
 * process-global lwIP access is serialized by ch_lwip_context.
 */
ch_tunnel_stack *ch_tunnel_stack_create(
    const ch_tunnel_stack_options *options, ch_error *error);
void ch_tunnel_stack_destroy(ch_tunnel_stack *stack);
ch_status ch_tunnel_stack_inject(ch_tunnel_stack *stack,
                                 const uint8_t *packet, size_t length,
                                 ch_error *error);
ch_status ch_tunnel_stack_dial_tcp(ch_tunnel_stack *stack,
                                   const char *target,
                                   int *out_descriptor,
                                   ch_error *error);

ch_status ch_tunnel_stack_open_packet(ch_tunnel_stack *stack,
                                      ch_tunnel_packet **out_packet,
                                      ch_error *error);
ch_status ch_tunnel_packet_send(ch_tunnel_packet *packet,
                                const char *target,
                                const uint8_t *payload,
                                size_t payload_length,
                                ch_error *error);
ch_status ch_tunnel_packet_receive(ch_tunnel_packet *packet,
                                   uint8_t *buffer,
                                   size_t buffer_capacity,
                                   size_t *out_length,
                                   char **out_source,
                                   int timeout_milliseconds,
                                   ch_error *error);
void ch_tunnel_packet_close(ch_tunnel_packet *packet);

#endif
