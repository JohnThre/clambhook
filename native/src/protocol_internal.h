// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_PROTOCOL_INTERNAL_H
#define CLAMBHOOK_PROTOCOL_INTERNAL_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"
#include "conditioner.h"
#include "tunnel_stack.h"

typedef struct ch_packet_connection ch_packet_connection;

/* Takes ownership of underlying_descriptor on every path. */
ch_status ch_protocol_tls_wrap(const ch_config_table *settings,
                               const char *server_address,
                               int underlying_descriptor,
                               const uint8_t *initial_payload,
                               size_t initial_payload_length,
                               const char *label,
                               int *out_descriptor,
                               ch_error *error);

/* Opens a Trojan/clambback UDP-associate TLS stream. */
ch_status ch_protocol_trojan_packet_stream(
    const ch_config_table *server,
    int underlying_descriptor,
    int *out_descriptor,
    ch_error *error);

/* Dials every carrier hop, then opens Trojan/clambback UDP on the final hop. */
ch_status ch_protocol_chain_trojan_packet_stream(
    const ch_config_table *chain,
    int *out_descriptor,
    ch_error *error);
ch_status ch_protocol_chain_vmess_packet_stream(
    const ch_config_table *chain,
    const char *target,
    int *out_descriptor,
    ch_error *error);

/* Takes ownership of packet on every path. */
ch_status ch_protocol_tunnel_packet_wrap(
    ch_tunnel_packet *packet,
    ch_packet_connection **out_connection,
    ch_error *error);

void ch_packet_connection_set_conditioner(
    ch_packet_connection *connection,
    const ch_conditioner_config *config);

#endif
