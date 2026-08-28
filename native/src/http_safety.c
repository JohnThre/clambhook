#include "http_safety.h"

#include <netdb.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>

#include <curl/curl.h>

#include "internal.h"

void ch_http_endpoint_clear(ch_http_endpoint *endpoint) {
    if (endpoint == NULL) return;
    curl_free(endpoint->url);
    curl_free(endpoint->scheme);
    curl_free(endpoint->host);
    curl_free(endpoint->port);
    free(endpoint->resolve);
    memset(endpoint, 0, sizeof(*endpoint));
}

static int http_ipv4_unsafe(const unsigned char address[4]) {
    return address[0] == 0U || address[0] == 10U || address[0] == 127U ||
        (address[0] == 172U && address[1] >= 16U && address[1] <= 31U) ||
        (address[0] == 192U && address[1] == 168U) ||
        (address[0] == 169U && address[1] == 254U) ||
        (address[0] == 100U && address[1] >= 64U && address[1] <= 127U) ||
        (address[0] == 192U && address[1] == 0U && address[2] == 0U) ||
        (address[0] == 192U && address[1] == 0U && address[2] == 2U) ||
        (address[0] == 192U && address[1] == 88U && address[2] == 99U) ||
        (address[0] == 198U && (address[1] & 0xfeU) == 18U) ||
        (address[0] == 198U && address[1] == 51U && address[2] == 100U) ||
        (address[0] == 203U && address[1] == 0U && address[2] == 113U) ||
        address[0] >= 224U;
}

static int http_sockaddr_unsafe(const struct sockaddr *address) {
    if (address == NULL) return 1;
    if (address->sa_family == AF_INET) {
        const struct sockaddr_in *ipv4 = (const struct sockaddr_in *)address;
        return http_ipv4_unsafe((const unsigned char *)&ipv4->sin_addr);
    }
    if (address->sa_family != AF_INET6) return 1;
    const struct sockaddr_in6 *ipv6 = (const struct sockaddr_in6 *)address;
    const unsigned char *bytes = (const unsigned char *)&ipv6->sin6_addr;
    static const unsigned char mapped[12] = {
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0xffU, 0xffU
    };
    if (memcmp(bytes, mapped, sizeof(mapped)) == 0) {
        return http_ipv4_unsafe(bytes + 12U);
    }
    static const unsigned char compatible[12] = {
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U
    };
    if (memcmp(bytes, compatible, sizeof(compatible)) == 0 &&
        (bytes[12] != 0U || bytes[13] != 0U || bytes[14] != 0U ||
         bytes[15] > 1U)) {
        return http_ipv4_unsafe(bytes + 12U);
    }
    static const unsigned char nat64[12] = {
        0x00U, 0x64U, 0xffU, 0x9bU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U
    };
    if (memcmp(bytes, nat64, sizeof(nat64)) == 0) {
        return http_ipv4_unsafe(bytes + 12U);
    }
    int unspecified = 1;
    for (size_t index = 0U; index < 16U; ++index) {
        if (bytes[index] != 0U) unspecified = 0;
    }
    int loopback = !unspecified;
    for (size_t index = 0U; index < 15U; ++index) {
        if (bytes[index] != 0U) loopback = 0;
    }
    if (bytes[15] != 1U) loopback = 0;
    if (unspecified || loopback || (bytes[0] & 0xfeU) == 0xfcU ||
        (bytes[0] == 0xfeU && (bytes[1] & 0xc0U) == 0x80U) ||
        bytes[0] == 0xffU) return 1;
    /* Conservatively allow native global unicast only. The well-known NAT64
     * prefix was handled above so IPv6-only Android networks still work. */
    if ((bytes[0] & 0xe0U) != 0x20U) return 1;
    return (bytes[0] == 0x20U && bytes[1] == 0x01U &&
            bytes[2] == 0x00U && bytes[3] == 0x00U) ||
        (bytes[0] == 0x20U && bytes[1] == 0x01U &&
         bytes[2] == 0x00U && bytes[3] == 0x02U) ||
        (bytes[0] == 0x20U && bytes[1] == 0x01U &&
         bytes[2] == 0x0dU && bytes[3] == 0xb8U) ||
        (bytes[0] == 0x20U && bytes[1] == 0x01U && bytes[2] == 0x00U &&
         ((bytes[3] & 0xf0U) == 0x10U ||
          (bytes[3] & 0xf0U) == 0x20U)) ||
        (bytes[0] == 0x20U && bytes[1] == 0x02U);
}

static int http_host_is_metadata(const char *host) {
    return strcasecmp(host, "metadata") == 0 ||
        strcasecmp(host, "instance-data") == 0 ||
        strcasecmp(host, "metadata.google.internal") == 0 ||
        strcasecmp(host, "metadata.azure.internal") == 0;
}

static int http_host_is_localhost(const char *host) {
    size_t length = strlen(host);
    static const char suffix[] = ".localhost";
    return strcasecmp(host, "localhost") == 0 ||
        (length > sizeof(suffix) - 1U &&
         strcasecmp(host + length - (sizeof(suffix) - 1U), suffix) == 0);
}

ch_status ch_http_endpoint_prepare(const char *url, const char *purpose,
                                   ch_http_endpoint *out, ch_error *error) {
    const char *label = purpose == NULL || purpose[0] == '\0' ?
        "request" : purpose;
    ch_error_clear(error);
    if (out == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "%s endpoint output is required", label);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out, 0, sizeof(*out));
    CURLU *parsed = curl_url();
    char *user = NULL;
    char *password = NULL;
    if (parsed == NULL || url == NULL ||
        curl_url_set(parsed, CURLUPART_URL, url, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_SCHEME, &out->scheme, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_HOST, &out->host, 0U) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_PORT, &out->port,
                     CURLU_DEFAULT_PORT) != CURLUE_OK ||
        curl_url_get(parsed, CURLUPART_URL, &out->url, 0U) != CURLUE_OK ||
        (strcasecmp(out->scheme, "http") != 0 &&
         strcasecmp(out->scheme, "https") != 0) || out->host[0] == '\0') {
        curl_url_cleanup(parsed);
        ch_http_endpoint_clear(out);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "%s URL must be http or https with a host", label);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    CURLUcode user_status = curl_url_get(parsed, CURLUPART_USER, &user, 0U);
    CURLUcode password_status = curl_url_get(
        parsed, CURLUPART_PASSWORD, &password, 0U);
    if (user_status == CURLUE_OK || password_status == CURLUE_OK) {
        curl_free(user);
        curl_free(password);
        curl_url_cleanup(parsed);
        ch_http_endpoint_clear(out);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "%s URL must not contain credentials", label);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    curl_free(user);
    curl_free(password);
    curl_url_cleanup(parsed);
    size_t host_length = strlen(out->host);
    if (host_length >= 2U && out->host[0] == '[' &&
        out->host[host_length - 1U] == ']') {
        memmove(out->host, out->host + 1U, host_length - 2U);
        out->host[host_length - 2U] = '\0';
        host_length -= 2U;
    }
    while (host_length > 0U && out->host[host_length - 1U] == '.') {
        out->host[--host_length] = '\0';
    }
    if (http_host_is_localhost(out->host) || http_host_is_metadata(out->host)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "%s host %s is not public", label, out->host);
        ch_http_endpoint_clear(out);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    struct addrinfo *addresses = NULL;
    int resolved = getaddrinfo(out->host, out->port, &hints, &addresses);
    if (resolved != 0 || addresses == NULL) {
        ch_error_set(error, CH_ERROR_IO, "resolve %s host %s: %s", label,
                     out->host, gai_strerror(resolved));
        ch_http_endpoint_clear(out);
        return CH_ERROR_IO;
    }
    char numeric[NI_MAXHOST];
    numeric[0] = '\0';
    ch_status status = CH_OK;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        if (http_sockaddr_unsafe(candidate->ai_addr)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "%s host %s resolves to a non-public address",
                         label, out->host);
            status = CH_ERROR_INVALID_ARGUMENT;
            break;
        }
        if (numeric[0] == '\0' &&
            getnameinfo(candidate->ai_addr, candidate->ai_addrlen, numeric,
                        sizeof(numeric), NULL, 0U, NI_NUMERICHOST) != 0) {
            ch_error_set(error, CH_ERROR_IO,
                         "format %s address for %s", label, out->host);
            status = CH_ERROR_IO;
            break;
        }
    }
    freeaddrinfo(addresses);
    if (status != CH_OK) {
        ch_http_endpoint_clear(out);
        return status;
    }
    int ipv6_host = strchr(out->host, ':') != NULL;
    int ipv6_address = strchr(numeric, ':') != NULL;
    int length = snprintf(
        NULL, 0,
        ipv6_host ? (ipv6_address ? "[%s]:%s:[%s]" : "[%s]:%s:%s") :
                    (ipv6_address ? "%s:%s:[%s]" : "%s:%s:%s"),
        out->host, out->port, numeric);
    out->resolve = length < 0 ? NULL : malloc((size_t)length + 1U);
    if (out->resolve == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate pinned %s address", label);
        ch_http_endpoint_clear(out);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(
        out->resolve, (size_t)length + 1U,
        ipv6_host ? (ipv6_address ? "[%s]:%s:[%s]" : "[%s]:%s:%s") :
                    (ipv6_address ? "%s:%s:[%s]" : "%s:%s:%s"),
        out->host, out->port, numeric);
    return CH_OK;
}

int ch_http_endpoint_same_origin(const ch_http_endpoint *first,
                                 const ch_http_endpoint *next) {
    return first != NULL && next != NULL &&
        strcasecmp(first->scheme, next->scheme) == 0 &&
        strcasecmp(first->host, next->host) == 0 &&
        strcmp(first->port, next->port) == 0;
}

char *ch_http_resolve_redirect(const char *base, const char *location) {
    CURLU *parsed = curl_url();
    char *resolved = NULL;
    if (parsed != NULL && base != NULL && location != NULL &&
        curl_url_set(parsed, CURLUPART_URL, base, 0U) == CURLUE_OK &&
        curl_url_set(parsed, CURLUPART_URL, location, 0U) == CURLUE_OK) {
        (void)curl_url_get(parsed, CURLUPART_URL, &resolved, 0U);
    }
    curl_url_cleanup(parsed);
    return resolved;
}
