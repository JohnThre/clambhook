#ifndef CLAMBHOOK_PROTOCOL_H
#define CLAMBHOOK_PROTOCOL_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Connects a TCP stream with the native dial timeout. */
ch_status ch_protocol_connect_tcp(const char *target, int *out_descriptor,
                                  ch_error *error);

/*
 * Dials target through every server in chain. The returned descriptor is a
 * normal full-duplex byte stream and belongs to the caller.
 */
ch_status ch_protocol_chain_dial(const ch_config_table *chain,
                                 const char *network, const char *target,
                                 int *out_descriptor, ch_error *error);

/* Exposed to freeze the Trojan/clambback opening-frame contract in tests. */
ch_status ch_protocol_trojan_header(const char *password, const char *target,
                                    uint8_t **out_header,
                                    size_t *out_header_length,
                                    ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
