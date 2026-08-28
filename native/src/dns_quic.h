#ifndef CLAMBHOOK_DNS_QUIC_H
#define CLAMBHOOK_DNS_QUIC_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/dns.h"

bool ch_dns_quic_available(void);

ch_status ch_dns_quic_exchange(
    const ch_dns_proxy_options *options,
    const char *target,
    const char *server_name,
    const char *const *bootstrap_ips,
    size_t bootstrap_ip_count,
    unsigned int timeout_milliseconds,
    const uint8_t *query,
    size_t query_length,
    uint8_t **out_response,
    size_t *out_response_length,
    ch_error *error
);

#endif
