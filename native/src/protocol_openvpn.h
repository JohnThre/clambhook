// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_PROTOCOL_OPENVPN_H
#define CLAMBHOOK_PROTOCOL_OPENVPN_H

#include "clambhook/config.h"
#include "clambhook/error.h"
#include "tunnel_stack.h"

ch_status ch_protocol_openvpn_dial(const ch_config_table *server,
                                   const char *target,
                                   int *out_descriptor,
                                   ch_error *error);
ch_status ch_protocol_openvpn_open_packet(
    const ch_config_table *server,
    ch_tunnel_packet **out_packet,
    ch_error *error);
void ch_protocol_openvpn_reset(void);

/* Deterministic protocol fixtures used by the native test binary. */
ch_status ch_protocol_openvpn_test_fixtures(ch_error *error);

#endif
