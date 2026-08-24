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

#endif
