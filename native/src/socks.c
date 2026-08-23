#include "clambhook/socks.h"

#include <arpa/inet.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "internal.h"

static ch_status ch_socks_split_address(
    const char *address,
    char **host,
    uint16_t *port,
    ch_error *error
) {
    const char *host_start = address;
    const char *host_end = NULL;
    const char *port_start = NULL;
    if (address[0] == '[') {
        host_start = address + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_PARSE, "socks: split host/port failed");
            return CH_ERROR_PARSE;
        }
        port_start = host_end + 2;
    } else {
        const char *separator = strrchr(address, ':');
        if (separator == NULL || separator == address || separator[1] == '\0' ||
            memchr(address, ':', (size_t)(separator - address)) != NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "socks: split host/port failed");
            return CH_ERROR_PARSE;
        }
        host_end = separator;
        port_start = separator + 1;
    }

    errno = 0;
    char *port_end = NULL;
    long parsed = strtol(port_start, &port_end, 10);
    if (errno != 0 || port_end == port_start || *port_end != '\0' || parsed < 0L || parsed > 65535L) {
        ch_error_set(error, CH_ERROR_PARSE, "socks: invalid port");
        return CH_ERROR_PARSE;
    }
    size_t host_length = (size_t)(host_end - host_start);
    if (host_length == 0U || host_length > 255U) {
        ch_error_set(error, CH_ERROR_PARSE, "socks: domain length out of range");
        return CH_ERROR_PARSE;
    }
    char *copy = malloc(host_length + 1U);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate SOCKS host");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(copy, host_start, host_length);
    copy[host_length] = '\0';
    *host = copy;
    *port = (uint16_t)parsed;
    return CH_OK;
}

ch_status ch_socks_encode_address(
    const char *address,
    uint8_t **encoded,
    size_t *encoded_length,
    ch_error *error
) {
    ch_error_clear(error);
    if (address == NULL || encoded == NULL || encoded_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "address and output pointers are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *encoded = NULL;
    *encoded_length = 0U;
    char *host = NULL;
    uint16_t port = 0U;
    ch_status status = ch_socks_split_address(address, &host, &port, error);
    if (status != CH_OK) {
        return status;
    }

    uint8_t raw[16];
    int family = 0;
    size_t address_length = 0U;
    size_t header_length = 1U;
    if (inet_pton(AF_INET, host, raw) == 1) {
        family = CH_SOCKS_ATYP_IPV4;
        address_length = 4U;
    } else if (inet_pton(AF_INET6, host, raw) == 1) {
        family = CH_SOCKS_ATYP_IPV6;
        address_length = 16U;
    } else {
        family = CH_SOCKS_ATYP_DOMAIN;
        address_length = strlen(host);
        header_length = 2U;
    }
    size_t total = header_length + address_length + 2U;
    uint8_t *output = malloc(total);
    if (output == NULL) {
        free(host);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate SOCKS address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    output[0] = (uint8_t)family;
    size_t offset = 1U;
    if (family == CH_SOCKS_ATYP_DOMAIN) {
        output[offset++] = (uint8_t)address_length;
        memcpy(output + offset, host, address_length);
    } else {
        memcpy(output + offset, raw, address_length);
    }
    offset += address_length;
    output[offset++] = (uint8_t)(port >> 8U);
    output[offset] = (uint8_t)(port & 0xffU);
    free(host);
    *encoded = output;
    *encoded_length = total;
    return CH_OK;
}

ch_status ch_socks_decode_address(
    const uint8_t *encoded,
    size_t encoded_length,
    char **host,
    uint16_t *port,
    size_t *consumed,
    ch_error *error
) {
    ch_error_clear(error);
    if (encoded == NULL || host == NULL || port == NULL || consumed == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "encoded address and output pointers are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *host = NULL;
    *port = 0U;
    *consumed = 0U;
    if (encoded_length < 1U) {
        ch_error_set(error, CH_ERROR_PARSE, "socks: read atyp: unexpected end of input");
        return CH_ERROR_PARSE;
    }
    size_t offset = 1U;
    char text[INET6_ADDRSTRLEN];
    const char *rendered = NULL;
    size_t domain_length = 0U;
    switch (encoded[0]) {
        case CH_SOCKS_ATYP_IPV4:
            if (encoded_length < offset + 4U + 2U) {
                ch_error_set(error, CH_ERROR_PARSE, "socks: read ipv4: unexpected end of input");
                return CH_ERROR_PARSE;
            }
            rendered = inet_ntop(AF_INET, encoded + offset, text, sizeof(text));
            offset += 4U;
            break;
        case CH_SOCKS_ATYP_IPV6:
            if (encoded_length < offset + 16U + 2U) {
                ch_error_set(error, CH_ERROR_PARSE, "socks: read ipv6: unexpected end of input");
                return CH_ERROR_PARSE;
            }
            rendered = inet_ntop(AF_INET6, encoded + offset, text, sizeof(text));
            offset += 16U;
            break;
        case CH_SOCKS_ATYP_DOMAIN:
            if (encoded_length < offset + 1U) {
                ch_error_set(error, CH_ERROR_PARSE, "socks: read domain len: unexpected end of input");
                return CH_ERROR_PARSE;
            }
            domain_length = encoded[offset++];
            if (domain_length == 0U) {
                ch_error_set(error, CH_ERROR_PARSE, "socks: empty domain");
                return CH_ERROR_PARSE;
            }
            if (encoded_length < offset + domain_length + 2U) {
                ch_error_set(error, CH_ERROR_PARSE, "socks: read domain: unexpected end of input");
                return CH_ERROR_PARSE;
            }
            break;
        default:
            ch_error_set(error, CH_ERROR_PARSE, "socks: unsupported atyp 0x%02x", encoded[0]);
            return CH_ERROR_PARSE;
    }
    if (rendered == NULL && domain_length == 0U) {
        ch_error_set(error, CH_ERROR_PARSE, "socks: invalid IP address");
        return CH_ERROR_PARSE;
    }
    if (domain_length > 0U) {
        *host = malloc(domain_length + 1U);
        if (*host != NULL) {
            memcpy(*host, encoded + offset, domain_length);
            (*host)[domain_length] = '\0';
        }
        offset += domain_length;
    } else {
        *host = ch_strdup(rendered);
    }
    if (*host == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate decoded SOCKS host");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *port = (uint16_t)(((uint16_t)encoded[offset] << 8U) | (uint16_t)encoded[offset + 1U]);
    offset += 2U;
    *consumed = offset;
    return CH_OK;
}

void ch_bytes_free(uint8_t *bytes) {
    free(bytes);
}
