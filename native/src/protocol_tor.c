// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "protocol_tor.h"

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/protocol.h"
#include "clambhook/socks.h"
#include "internal.h"

static void ch_tor_close(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static ssize_t ch_tor_send(int descriptor, const void *bytes, size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static int ch_tor_send_all(int descriptor, const void *bytes, size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = ch_tor_send(descriptor, cursor, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_tor_receive_exact(int descriptor, void *bytes, size_t length) {
    uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t received = recv(descriptor, cursor, length, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) return 0;
        cursor += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static char *ch_tor_optional_string(const ch_config_table *table,
                                    const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static const char *ch_tor_reply_text(uint8_t reply) {
    switch (reply) {
        case 0x01U: return "general SOCKS server failure";
        case 0x02U: return "connection not allowed by ruleset";
        case 0x03U: return "network unreachable";
        case 0x04U: return "host unreachable";
        case 0x05U: return "connection refused";
        case 0x06U: return "TTL expired";
        case 0x07U: return "command not supported";
        case 0x08U: return "address type not supported";
        default: return "unknown SOCKS5 reply code";
    }
}

static ch_status ch_tor_handshake(int descriptor, const char *target,
                                  const char *user, const char *password,
                                  ch_error *error) {
    size_t user_length = strlen(user);
    size_t password_length = strlen(password);
    uint8_t greeting_with_auth[] = {0x05U, 0x02U, 0x00U, 0x02U};
    uint8_t greeting_no_auth[] = {0x05U, 0x01U, 0x00U};
    const uint8_t *greeting = user_length > 0U ? greeting_with_auth :
                              greeting_no_auth;
    size_t greeting_length = user_length > 0U ? sizeof(greeting_with_auth) :
                             sizeof(greeting_no_auth);
    uint8_t method[2];
    if (!ch_tor_send_all(descriptor, greeting, greeting_length) ||
        !ch_tor_receive_exact(descriptor, method, sizeof(method))) {
        ch_error_set(error, CH_ERROR_IO, "tor: SOCKS5 method exchange failed");
        return CH_ERROR_IO;
    }
    if (method[0] != 0x05U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "tor: bad SOCKS version in method reply");
        return CH_ERROR_PARSE;
    }
    if (method[1] == 0xffU) {
        ch_error_set(error, CH_ERROR_IO,
                     "tor: SOCKS5 server rejected all offered methods");
        return CH_ERROR_IO;
    }
    if (method[1] == 0x02U) {
        if (user_length == 0U) {
            ch_error_set(error, CH_ERROR_IO,
                         "tor: server selected user/pass without credentials");
            return CH_ERROR_IO;
        }
        size_t auth_length = 3U + user_length + password_length;
        uint8_t *auth = malloc(auth_length);
        if (auth == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate Tor isolation credentials");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        size_t offset = 0U;
        auth[offset++] = 0x01U;
        auth[offset++] = (uint8_t)user_length;
        memcpy(auth + offset, user, user_length);
        offset += user_length;
        auth[offset++] = (uint8_t)password_length;
        memcpy(auth + offset, password, password_length);
        uint8_t auth_reply[2];
        int exchanged = ch_tor_send_all(descriptor, auth, auth_length) &&
                        ch_tor_receive_exact(descriptor, auth_reply,
                                             sizeof(auth_reply));
        free(auth);
        if (!exchanged) {
            ch_error_set(error, CH_ERROR_IO,
                         "tor: user/pass exchange failed");
            return CH_ERROR_IO;
        }
        if (auth_reply[0] != 0x01U || auth_reply[1] != 0x00U) {
            ch_error_set(error, CH_ERROR_IO,
                         "tor: user/pass authentication failed");
            return CH_ERROR_IO;
        }
    } else if (method[1] != 0x00U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "tor: SOCKS5 server selected an unsupported method");
        return CH_ERROR_UNSUPPORTED;
    }

    uint8_t *address = NULL;
    size_t address_length = 0U;
    ch_status status = ch_socks_encode_address(target, &address,
                                               &address_length, error);
    if (status != CH_OK) return status;
    uint8_t *request = malloc(3U + address_length);
    if (request == NULL) {
        free(address);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Tor CONNECT request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    request[0] = 0x05U;
    request[1] = 0x01U;
    request[2] = 0x00U;
    memcpy(request + 3U, address, address_length);
    free(address);
    int sent = ch_tor_send_all(descriptor, request, 3U + address_length);
    free(request);
    uint8_t reply[4];
    if (!sent || !ch_tor_receive_exact(descriptor, reply, sizeof(reply))) {
        ch_error_set(error, CH_ERROR_IO, "tor: CONNECT exchange failed");
        return CH_ERROR_IO;
    }
    if (reply[0] != 0x05U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "tor: bad SOCKS version in CONNECT reply");
        return CH_ERROR_PARSE;
    }
    size_t address_tail = 0U;
    if (reply[3] == CH_SOCKS_ATYP_IPV4) {
        address_tail = 4U + 2U;
    } else if (reply[3] == CH_SOCKS_ATYP_IPV6) {
        address_tail = 16U + 2U;
    } else if (reply[3] == CH_SOCKS_ATYP_DOMAIN) {
        uint8_t domain_length = 0U;
        if (!ch_tor_receive_exact(descriptor, &domain_length, 1U)) {
            ch_error_set(error, CH_ERROR_IO,
                         "tor: read bound domain length failed");
            return CH_ERROR_IO;
        }
        address_tail = (size_t)domain_length + 2U;
    } else {
        ch_error_set(error, CH_ERROR_PARSE,
                     "tor: unsupported bound address type");
        return CH_ERROR_PARSE;
    }
    uint8_t tail[257];
    if (address_tail > sizeof(tail) ||
        !ch_tor_receive_exact(descriptor, tail, address_tail)) {
        ch_error_set(error, CH_ERROR_IO,
                     "tor: drain CONNECT reply failed");
        return CH_ERROR_IO;
    }
    if (reply[1] != 0x00U) {
        ch_error_set(error, CH_ERROR_IO, "tor: CONNECT failed: %s",
                     ch_tor_reply_text(reply[1]));
        return CH_ERROR_IO;
    }
    return CH_OK;
}

ch_status ch_protocol_tor_dial(const ch_config_table *server,
                               int underlying_descriptor,
                               const char *target,
                               int *out_descriptor,
                               ch_error *error) {
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    char *address = ch_tor_optional_string(server, "address");
    char *user = ch_tor_optional_string(settings, "isolation_user");
    char *password = ch_tor_optional_string(settings, "isolation_pass");
    if (address == NULL || user == NULL || password == NULL) {
        free(address); free(user); free(password);
        ch_tor_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Tor configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (address[0] == '\0' || (user[0] == '\0') != (password[0] == '\0') ||
        strlen(user) > 255U || strlen(password) > 255U) {
        const char *message = address[0] == '\0' ?
            "tor: address is required" :
            "tor: isolation_user and isolation_pass must be set together "
            "and fit RFC 1929";
        free(address); free(user); free(password);
        ch_tor_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "%s", message);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *validated_address = NULL;
    size_t validated_address_length = 0U;
    ch_status status = ch_socks_encode_address(
        address, &validated_address, &validated_address_length, error);
    free(validated_address);
    if (status != CH_OK) {
        free(address); free(user); free(password);
        ch_tor_close(&underlying_descriptor);
        return status;
    }
    if (underlying_descriptor < 0) {
        status = ch_protocol_connect_tcp(address, &underlying_descriptor,
                                         error);
    }
    free(address);
    if (status == CH_OK) {
        status = ch_tor_handshake(underlying_descriptor, target, user,
                                  password, error);
    }
    free(user);
    free(password);
    if (status != CH_OK) {
        ch_tor_close(&underlying_descriptor);
        return status;
    }
    *out_descriptor = underlying_descriptor;
    return CH_OK;
}
