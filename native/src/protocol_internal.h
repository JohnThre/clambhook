#ifndef CLAMBHOOK_PROTOCOL_INTERNAL_H
#define CLAMBHOOK_PROTOCOL_INTERNAL_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

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

#endif
