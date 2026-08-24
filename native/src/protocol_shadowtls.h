#ifndef CLAMBHOOK_PROTOCOL_SHADOWTLS_H
#define CLAMBHOOK_PROTOCOL_SHADOWTLS_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

ch_status ch_shadowtls_signature(const char *password,
                                 const uint8_t *client_hello,
                                 size_t client_hello_length,
                                 uint8_t signature[4],
                                 ch_error *error);

bool ch_shadowtls_server_hello_tls13(const uint8_t *server_hello,
                                     size_t server_hello_length);

ch_status ch_protocol_shadowtls_dial(const ch_config_table *server,
                                     int underlying_descriptor,
                                     int *out_descriptor,
                                     ch_error *error);

#endif
