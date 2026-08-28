// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_PROTOCOL_WIREGUARD_H
#define CLAMBHOOK_PROTOCOL_WIREGUARD_H

#include "clambhook/config.h"
#include "clambhook/error.h"
#include "tunnel_stack.h"

ch_status ch_protocol_wireguard_dial(const ch_config_table *server,
                                     const char *target,
                                     int *out_descriptor,
                                     ch_error *error);
ch_status ch_protocol_wireguard_open_packet(
    const ch_config_table *server,
    ch_tunnel_packet **out_packet,
    ch_error *error);

/* Closes reusable layer-3 protocol sessions on runtime stop or reload. */
void ch_protocol_wireguard_reset(void);

#endif
