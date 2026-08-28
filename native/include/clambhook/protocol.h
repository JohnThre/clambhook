#ifndef CLAMBHOOK_PROTOCOL_H
#define CLAMBHOOK_PROTOCOL_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_packet_connection ch_packet_connection;

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
/* Same chain dial with a bounded connect and handshake timeout. */
ch_status ch_protocol_chain_dial_timeout(const ch_config_table *chain,
                                         const char *network,
                                         const char *target,
                                         unsigned int timeout_milliseconds,
                                         int *out_descriptor,
                                         ch_error *error);

/*
 * Opens a datagram path for a native direct or single-hop Shadowsocks chain.
 * Each send carries its own host:port target. Each receive returns a newly
 * allocated source string that must be released with free().
 */
ch_status ch_protocol_chain_dial_packet(const ch_config_table *chain,
                                        const char *initial_target,
                                        ch_packet_connection **out_connection,
                                        ch_error *error);
/* Performs the same structural UDP support check as packet dialing without
 * opening a socket. This is used by policy groups before choosing a member. */
ch_status ch_protocol_chain_supports_packet(const ch_config_table *chain,
                                            ch_error *error);
ch_status ch_protocol_direct_packet_dial(
    ch_packet_connection **out_connection,
    ch_error *error);
ch_status ch_packet_connection_send(ch_packet_connection *connection,
                                    const char *target,
                                    const uint8_t *payload,
                                    size_t payload_length,
                                    ch_error *error);
ch_status ch_packet_connection_receive(ch_packet_connection *connection,
                                       uint8_t *buffer,
                                       size_t buffer_capacity,
                                       size_t *out_length,
                                       char **out_source,
                                       ch_error *error);
ch_status ch_packet_connection_receive_timeout(
    ch_packet_connection *connection,
    uint8_t *buffer,
    size_t buffer_capacity,
    size_t *out_length,
    char **out_source,
    int timeout_milliseconds,
    ch_error *error);
void ch_packet_connection_close(ch_packet_connection *connection);

/* Tears down reusable layer-3 sessions after runtime stop or reload. */
void ch_protocol_reset_sessions(void);

/* Exposed to freeze the Trojan/clambback opening-frame contract in tests. */
ch_status ch_protocol_trojan_header(const char *password, const char *target,
                                    uint8_t **out_header,
                                    size_t *out_header_length,
                                    ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
