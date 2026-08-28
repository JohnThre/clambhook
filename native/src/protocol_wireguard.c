#include "protocol_wireguard.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <openssl/evp.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <unistd.h>

#include "crypto.h"
#include "internal.h"
#include "wireguard.h"

#define CH_WG_DEFAULT_MTU 1420U
#define CH_WG_MAX_INNER_PACKET 65535U
#define CH_WG_PENDING_PACKETS 128U
#define CH_WG_PENDING_BYTES (1024U * 1024U)
#define CH_WG_POLL_MILLISECONDS 100

typedef struct ch_wg_prefix {
    int family;
    uint8_t address[16];
    unsigned int length;
} ch_wg_prefix;

typedef struct ch_wg_pending {
    struct ch_wg_pending *next;
    uint8_t *bytes;
    size_t length;
} ch_wg_pending;

typedef struct ch_wg_peer {
    struct wireguard_peer *protocol;
    struct sockaddr_storage configured_endpoint;
    socklen_t configured_endpoint_length;
    struct sockaddr_storage endpoint;
    socklen_t endpoint_length;
    ch_wg_prefix allowed[WIREGUARD_MAX_SRC_IPS];
    size_t allowed_count;
    ch_wg_pending *pending_head;
    ch_wg_pending *pending_tail;
    size_t pending_count;
    size_t pending_bytes;
} ch_wg_peer;

typedef struct ch_wg_session {
    struct ch_wg_session *next;
    char *key;
    struct wireguard_device device;
    ch_wg_peer peers[WIREGUARD_MAX_PEERS];
    size_t peer_count;
    char **addresses;
    size_t address_count;
    char **dns_servers;
    size_t dns_server_count;
    unsigned int mtu;
    int ipv4_socket;
    int ipv6_socket;
    int wake_read;
    int wake_write;
    pthread_mutex_t mutex;
    pthread_t worker;
    int worker_started;
    int stopping;
    ch_tunnel_stack *tunnel;
} ch_wg_session;

static pthread_mutex_t ch_wg_registry_mutex = PTHREAD_MUTEX_INITIALIZER;
static ch_wg_session *ch_wg_registry;
static pthread_once_t ch_wg_once = PTHREAD_ONCE_INIT;

static void ch_wg_initialize_protocol(void) {
    wireguard_init();
}

static void ch_wg_close_descriptor(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static int ch_wg_set_nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL, 0);
    return flags >= 0 && fcntl(descriptor, F_SETFL,
                               flags | O_NONBLOCK) == 0;
}

static char *ch_wg_optional_string(const ch_config_table *table,
                                   const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || !ch_config_table_has(table, key) ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return NULL;
    }
    return value;
}

static int ch_wg_decode_key(const char *text, uint8_t output[32]) {
    if (text == NULL || strlen(text) != 44U || text[43] != '=') return 0;
    uint8_t decoded[33];
    int length = EVP_DecodeBlock(decoded, (const unsigned char *)text, 44);
    if (length != 33) {
        crypto_zero(decoded, sizeof(decoded));
        return 0;
    }
    char encoded[45];
    int encoded_length = EVP_EncodeBlock((unsigned char *)encoded,
                                         decoded, 32);
    int valid = encoded_length == 44 && memcmp(encoded, text, 44U) == 0;
    if (valid) memcpy(output, decoded, 32U);
    crypto_zero(decoded, sizeof(decoded));
    crypto_zero(encoded, sizeof(encoded));
    return valid;
}

static int ch_wg_parse_prefix(const char *value, ch_wg_prefix *out) {
    if (value == NULL || out == NULL) return 0;
    const char *slash = strchr(value, '/');
    if (slash == NULL || slash == value || slash[1] == '\0') return 0;
    size_t address_length = (size_t)(slash - value);
    if (address_length >= INET6_ADDRSTRLEN) return 0;
    char address[INET6_ADDRSTRLEN];
    memcpy(address, value, address_length);
    address[address_length] = '\0';
    int family = strchr(address, ':') == NULL ? AF_INET : AF_INET6;
    size_t bytes = family == AF_INET ? 4U : 16U;
    memset(out, 0, sizeof(*out));
    if (inet_pton(family, address, out->address) != 1) return 0;
    char *end = NULL;
    errno = 0;
    unsigned long prefix = strtoul(slash + 1U, &end, 10);
    unsigned long maximum = family == AF_INET ? 32UL : 128UL;
    if (errno != 0 || end == slash + 1U || *end != '\0' ||
        prefix > maximum) return 0;
    out->family = family;
    out->length = (unsigned int)prefix;
    unsigned int full = out->length / 8U;
    unsigned int partial = out->length % 8U;
    if (partial != 0U && full < bytes) {
        out->address[full] &= (uint8_t)(0xffU << (8U - partial));
        ++full;
    }
    if (full < bytes) memset(out->address + full, 0, bytes - full);
    return 1;
}

static int ch_wg_prefix_matches(const ch_wg_prefix *prefix, int family,
                                const uint8_t *address) {
    if (prefix == NULL || address == NULL || prefix->family != family) {
        return 0;
    }
    unsigned int full = prefix->length / 8U;
    unsigned int partial = prefix->length % 8U;
    if (full > 0U && memcmp(prefix->address, address, full) != 0) return 0;
    if (partial == 0U) return 1;
    uint8_t mask = (uint8_t)(0xffU << (8U - partial));
    return (prefix->address[full] & mask) == (address[full] & mask);
}

static int ch_wg_split_endpoint(const char *value, char **out_host,
                                char **out_service) {
    *out_host = NULL;
    *out_service = NULL;
    if (value == NULL || value[0] == '\0') return 0;
    const char *host_start = value;
    const char *host_end = NULL;
    const char *service = NULL;
    if (value[0] == '[') {
        host_start = value + 1U;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            return 0;
        }
        service = host_end + 2U;
    } else {
        host_end = strrchr(value, ':');
        if (host_end == NULL || host_end == value || host_end[1] == '\0' ||
            strchr(value, ':') != host_end) return 0;
        service = host_end + 1U;
    }
    size_t host_length = (size_t)(host_end - host_start);
    char *host = malloc(host_length + 1U);
    char *port = ch_strdup(service);
    if (host == NULL || port == NULL) {
        free(host);
        free(port);
        return 0;
    }
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    *out_host = host;
    *out_service = port;
    return 1;
}

static int ch_wg_resolve_endpoint(const char *value,
                                  struct sockaddr_storage *out,
                                  socklen_t *out_length) {
    char *host = NULL;
    char *service = NULL;
    if (!ch_wg_split_endpoint(value, &host, &service)) return 0;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_DGRAM;
    hints.ai_protocol = IPPROTO_UDP;
    struct addrinfo *addresses = NULL;
    int resolved = getaddrinfo(host, service, &hints, &addresses);
    free(host);
    free(service);
    if (resolved != 0 || addresses == NULL ||
        addresses->ai_addrlen > sizeof(*out)) {
        if (addresses != NULL) freeaddrinfo(addresses);
        return 0;
    }
    memset(out, 0, sizeof(*out));
    memcpy(out, addresses->ai_addr, addresses->ai_addrlen);
    *out_length = (socklen_t)addresses->ai_addrlen;
    freeaddrinfo(addresses);
    return 1;
}

static void ch_wg_free_strings(char **values, size_t count) {
    for (size_t index = 0U; index < count; ++index) free(values[index]);
    free(values);
}

static int ch_wg_copy_string_array(const ch_config_array *array,
                                   int require_cidr,
                                   char ***out_values,
                                   size_t *out_count,
                                   ch_error *error,
                                   const char *label) {
    *out_values = NULL;
    *out_count = 0U;
    size_t count = ch_config_array_count(array);
    if (count == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: %s is required", label);
        return 0;
    }
    char **values = calloc(count, sizeof(*values));
    if (values == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "wireguard: allocate %s", label);
        return 0;
    }
    for (size_t index = 0U; index < count; ++index) {
        ch_error item_error;
        ch_error_clear(&item_error);
        if (ch_config_array_get_string(array, index, &values[index],
                                       &item_error) != CH_OK ||
            values[index] == NULL || values[index][0] == '\0') {
            ch_wg_free_strings(values, count);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "wireguard: %s entries must be strings", label);
            return 0;
        }
        ch_wg_prefix prefix;
        uint8_t ip[16];
        int valid = require_cidr ? ch_wg_parse_prefix(values[index], &prefix) :
            (inet_pton(AF_INET, values[index], ip) == 1 ||
             inet_pton(AF_INET6, values[index], ip) == 1);
        if (!valid) {
            char invalid[INET6_ADDRSTRLEN + 8U];
            (void)snprintf(invalid, sizeof(invalid), "%s", values[index]);
            ch_wg_free_strings(values, count);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "wireguard: invalid %s %s", label, invalid);
            return 0;
        }
    }
    *out_values = values;
    *out_count = count;
    return 1;
}

static void ch_wg_pending_clear(ch_wg_peer *peer) {
    ch_wg_pending *packet = peer->pending_head;
    while (packet != NULL) {
        ch_wg_pending *next = packet->next;
        free(packet->bytes);
        free(packet);
        packet = next;
    }
    peer->pending_head = NULL;
    peer->pending_tail = NULL;
    peer->pending_count = 0U;
    peer->pending_bytes = 0U;
}

static int ch_wg_create_socket(int family) {
    int descriptor = socket(family, SOCK_DGRAM, IPPROTO_UDP);
    if (descriptor < 0) return -1;
    if (family == AF_INET6) {
        int enabled = 1;
        (void)setsockopt(descriptor, IPPROTO_IPV6, IPV6_V6ONLY, &enabled,
                         (socklen_t)sizeof(enabled));
    }
    if (!ch_wg_set_nonblocking(descriptor)) {
        ch_wg_close_descriptor(&descriptor);
        return -1;
    }
    if (family == AF_INET) {
        struct sockaddr_in local;
        memset(&local, 0, sizeof(local));
        local.sin_family = AF_INET;
        local.sin_addr.s_addr = htonl(INADDR_ANY);
        if (bind(descriptor, (const struct sockaddr *)&local,
                 (socklen_t)sizeof(local)) != 0) {
            ch_wg_close_descriptor(&descriptor);
            return -1;
        }
    } else {
        struct sockaddr_in6 local;
        memset(&local, 0, sizeof(local));
        local.sin6_family = AF_INET6;
        local.sin6_addr = in6addr_any;
        if (bind(descriptor, (const struct sockaddr *)&local,
                 (socklen_t)sizeof(local)) != 0) {
            ch_wg_close_descriptor(&descriptor);
            return -1;
        }
    }
    return descriptor;
}

static int ch_wg_configure_peer(ch_wg_session *session,
                                const ch_config_table *peer_table,
                                size_t index,
                                char **out_endpoint,
                                ch_error *error) {
    *out_endpoint = NULL;
    char *public_text = ch_wg_optional_string(peer_table, "public_key");
    char *preshared_text = ch_wg_optional_string(peer_table,
                                                  "preshared_key");
    char *endpoint = ch_wg_optional_string(peer_table, "endpoint");
    uint8_t public_key[32];
    uint8_t preshared_key[32];
    memset(public_key, 0, sizeof(public_key));
    memset(preshared_key, 0, sizeof(preshared_key));
    if (public_text == NULL || public_text[0] == '\0' ||
        !ch_wg_decode_key(public_text, public_key)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peer %zu: public_key must be a base64 "
                     "32-byte key", index);
        goto fail;
    }
    const uint8_t *preshared = NULL;
    if (preshared_text != NULL && preshared_text[0] != '\0') {
        if (!ch_wg_decode_key(preshared_text, preshared_key)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "wireguard: peer %zu: preshared_key must be a "
                         "base64 32-byte key", index);
            goto fail;
        }
        preshared = preshared_key;
    }
    if (endpoint == NULL || endpoint[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peer %zu: endpoint is required", index);
        goto fail;
    }
    ch_wg_peer *configured = &session->peers[index];
    if (!ch_wg_resolve_endpoint(endpoint, &configured->endpoint,
                                &configured->endpoint_length)) {
        ch_error_set(error, CH_ERROR_IO,
                     "wireguard: peer %zu: resolve endpoint %s", index,
                     endpoint);
        goto fail;
    }
    configured->configured_endpoint = configured->endpoint;
    configured->configured_endpoint_length = configured->endpoint_length;
    const ch_config_array *allowed = ch_config_table_get_array(
        peer_table, "allowed_ips");
    size_t allowed_count = ch_config_array_count(allowed);
    if (allowed_count == 0U || allowed_count > WIREGUARD_MAX_SRC_IPS) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peer %zu: allowed_ips requires 1..%u "
                     "CIDRs", index, (unsigned int)WIREGUARD_MAX_SRC_IPS);
        goto fail;
    }
    for (size_t allowed_index = 0U; allowed_index < allowed_count;
         ++allowed_index) {
        char *value = NULL;
        ch_error ignored;
        if (ch_config_array_get_string(allowed, allowed_index, &value,
                                       &ignored) != CH_OK ||
            value == NULL || !ch_wg_parse_prefix(
                value, &configured->allowed[allowed_index])) {
            free(value);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "wireguard: peer %zu: allowed_ips entries must be "
                         "valid CIDRs", index);
            goto fail;
        }
        free(value);
    }
    configured->allowed_count = allowed_count;
    int64_t keepalive = 0;
    if (ch_config_table_has(peer_table, "persistent_keepalive") &&
        (ch_config_table_get_int(peer_table, "persistent_keepalive",
                                 &keepalive, error) != CH_OK ||
         keepalive < 0 || keepalive > UINT16_MAX)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peer %zu: persistent_keepalive is out of "
                     "range [0,65535]", index);
        goto fail;
    }
    struct wireguard_peer *protocol = peer_alloc(&session->device);
    if (protocol == NULL || !wireguard_peer_init(
            &session->device, protocol, public_key, preshared)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peer %zu: invalid public key", index);
        goto fail;
    }
    configured->protocol = protocol;
    protocol->active = true;
    protocol->keepalive_interval = (uint16_t)keepalive;
    *out_endpoint = endpoint;
    endpoint = NULL;
    crypto_zero(public_key, sizeof(public_key));
    crypto_zero(preshared_key, sizeof(preshared_key));
    free(public_text);
    free(preshared_text);
    return 1;

fail:
    crypto_zero(public_key, sizeof(public_key));
    crypto_zero(preshared_key, sizeof(preshared_key));
    free(public_text);
    free(preshared_text);
    free(endpoint);
    return 0;
}

static int ch_wg_configure_session(ch_wg_session *session,
                                   const ch_config_table *server,
                                   ch_error *error) {
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    if (settings == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: settings are required");
        return 0;
    }
    char *private_text = ch_wg_optional_string(settings, "private_key");
    uint8_t private_key[32];
    memset(private_key, 0, sizeof(private_key));
    if (private_text == NULL || private_text[0] == '\0' ||
        !ch_wg_decode_key(private_text, private_key)) {
        free(private_text);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: private_key must be a base64 32-byte key");
        return 0;
    }
    free(private_text);
    (void)pthread_once(&ch_wg_once, ch_wg_initialize_protocol);
    if (!wireguard_device_init(&session->device, private_key)) {
        crypto_zero(private_key, sizeof(private_key));
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: private_key is not a usable X25519 key");
        return 0;
    }
    crypto_zero(private_key, sizeof(private_key));

    if (!ch_wg_copy_string_array(ch_config_table_get_array(
            settings, "addresses"), 1, &session->addresses,
            &session->address_count, error, "addresses")) return 0;
    const ch_config_array *dns = ch_config_table_get_array(settings, "dns");
    if (ch_config_array_count(dns) > 0U && !ch_wg_copy_string_array(
            dns, 0, &session->dns_servers, &session->dns_server_count,
            error, "dns")) return 0;

    session->mtu = CH_WG_DEFAULT_MTU;
    if (ch_config_table_has(settings, "mtu")) {
        int64_t mtu = 0;
        if (ch_config_table_get_int(settings, "mtu", &mtu, error) != CH_OK ||
            mtu < 1280 || mtu > UINT16_MAX) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "wireguard: mtu must be between 1280 and 65535");
            return 0;
        }
        session->mtu = (unsigned int)mtu;
    }
    char *log_level = ch_wg_optional_string(settings, "log_level");
    if (log_level != NULL && strcasecmp(log_level, "silent") != 0 &&
        strcasecmp(log_level, "error") != 0 &&
        strcasecmp(log_level, "verbose") != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: invalid log_level %s "
                     "(want silent|error|verbose)", log_level);
        free(log_level);
        return 0;
    }
    free(log_level);

    const ch_config_array *peers = ch_config_table_get_array(settings,
                                                             "peers");
    size_t peer_count = ch_config_array_count(peers);
    if (peer_count == 0U || peer_count > WIREGUARD_MAX_PEERS) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: peers requires 1..%u entries",
                     (unsigned int)WIREGUARD_MAX_PEERS);
        return 0;
    }
    char *first_endpoint = NULL;
    for (size_t index = 0U; index < peer_count; ++index) {
        const ch_config_table *peer = ch_config_array_get_table(peers, index);
        char *endpoint = NULL;
        if (peer == NULL || !ch_wg_configure_peer(
                session, peer, index, &endpoint, error)) {
            free(endpoint);
            free(first_endpoint);
            return 0;
        }
        if (index == 0U) first_endpoint = endpoint;
        else free(endpoint);
    }
    session->peer_count = peer_count;
    char *server_address = ch_wg_optional_string(server, "address");
    if (server_address != NULL && server_address[0] != '\0' &&
        strcmp(server_address, first_endpoint) != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: server.address %s does not match "
                     "peers[0].endpoint %s", server_address,
                     first_endpoint);
        free(server_address);
        free(first_endpoint);
        return 0;
    }
    free(server_address);
    free(first_endpoint);

    int need_ipv4 = 0;
    int need_ipv6 = 0;
    for (size_t index = 0U; index < peer_count; ++index) {
        int family = session->peers[index].endpoint.ss_family;
        if (family == AF_INET) need_ipv4 = 1;
        else if (family == AF_INET6) need_ipv6 = 1;
    }
    if (need_ipv4) session->ipv4_socket = ch_wg_create_socket(AF_INET);
    if (need_ipv6) session->ipv6_socket = ch_wg_create_socket(AF_INET6);
    if ((need_ipv4 && session->ipv4_socket < 0) ||
        (need_ipv6 && session->ipv6_socket < 0)) {
        ch_error_set(error, CH_ERROR_IO,
                     "wireguard: create UDP transport: %s", strerror(errno));
        return 0;
    }
    return 1;
}

static ch_wg_peer *ch_wg_peer_for_protocol(ch_wg_session *session,
                                           struct wireguard_peer *protocol) {
    for (size_t index = 0U; index < session->peer_count; ++index) {
        if (session->peers[index].protocol == protocol) {
            return &session->peers[index];
        }
    }
    return NULL;
}

static int ch_wg_send_outer(ch_wg_session *session, ch_wg_peer *peer,
                            const void *bytes, size_t length) {
    int descriptor = peer->endpoint.ss_family == AF_INET6 ?
        session->ipv6_socket : session->ipv4_socket;
    if (descriptor < 0) return 0;
    ssize_t written;
    do {
        written = sendto(descriptor, bytes, length, 0,
                         (const struct sockaddr *)&peer->endpoint,
                         peer->endpoint_length);
    } while (written < 0 && errno == EINTR);
    return written == (ssize_t)length;
}

static void ch_wg_update_endpoint(ch_wg_peer *peer,
                                  const struct sockaddr_storage *endpoint,
                                  socklen_t endpoint_length) {
    if (endpoint_length <= sizeof(peer->endpoint)) {
        peer->endpoint = *endpoint;
        peer->endpoint_length = endpoint_length;
    }
}

static int ch_wg_inner_addresses(const uint8_t *packet, size_t length,
                                 int *out_family,
                                 const uint8_t **out_source,
                                 const uint8_t **out_destination,
                                 size_t *out_packet_length) {
    if (packet == NULL || length < 20U) return 0;
    unsigned int version = packet[0] >> 4U;
    if (version == 4U) {
        size_t header = (size_t)(packet[0] & 0x0fU) * 4U;
        size_t total = ((size_t)packet[2] << 8U) | packet[3];
        if (header < 20U || total < header || total > length) return 0;
        *out_family = AF_INET;
        *out_source = packet + 12U;
        *out_destination = packet + 16U;
        *out_packet_length = total;
        return 1;
    }
    if (version == 6U && length >= 40U) {
        size_t total = 40U + (((size_t)packet[4] << 8U) | packet[5]);
        if (total > length) return 0;
        *out_family = AF_INET6;
        *out_source = packet + 8U;
        *out_destination = packet + 24U;
        *out_packet_length = total;
        return 1;
    }
    return 0;
}

static ch_wg_peer *ch_wg_route_peer(ch_wg_session *session,
                                    int family,
                                    const uint8_t *destination) {
    ch_wg_peer *selected = NULL;
    unsigned int selected_length = 0U;
    for (size_t peer_index = 0U; peer_index < session->peer_count;
         ++peer_index) {
        ch_wg_peer *peer = &session->peers[peer_index];
        for (size_t prefix_index = 0U;
             prefix_index < peer->allowed_count; ++prefix_index) {
            ch_wg_prefix *prefix = &peer->allowed[prefix_index];
            if (ch_wg_prefix_matches(prefix, family, destination) &&
                (selected == NULL || prefix->length > selected_length)) {
                selected = peer;
                selected_length = prefix->length;
            }
        }
    }
    return selected;
}

static int ch_wg_source_allowed(const ch_wg_peer *peer, int family,
                                const uint8_t *source) {
    for (size_t index = 0U; index < peer->allowed_count; ++index) {
        if (ch_wg_prefix_matches(&peer->allowed[index], family, source)) {
            return 1;
        }
    }
    return 0;
}

static struct wireguard_keypair *ch_wg_sending_key(ch_wg_peer *peer) {
    struct wireguard_keypair *keypair = &peer->protocol->curr_keypair;
    if (keypair->valid && !keypair->initiator && keypair->last_rx == 0U) {
        keypair = &peer->protocol->prev_keypair;
    }
    if (!keypair->valid || (!keypair->initiator && keypair->last_rx == 0U) ||
        wireguard_expired(keypair->keypair_millis, REJECT_AFTER_TIME) ||
        keypair->sending_counter >= REJECT_AFTER_MESSAGES) {
        return NULL;
    }
    return keypair;
}

static int ch_wg_send_transport_locked(ch_wg_session *session,
                                       ch_wg_peer *peer,
                                       const uint8_t *packet,
                                       size_t packet_length) {
    struct wireguard_keypair *keypair = ch_wg_sending_key(peer);
    if (keypair == NULL || packet_length > CH_WG_MAX_INNER_PACKET ||
        packet_length > SIZE_MAX - 15U) return 0;
    size_t padded = (packet_length + 15U) & ~(size_t)15U;
    if (padded > 65535U - 32U) return 0;
    size_t outer_length = 16U + padded + WIREGUARD_AUTHTAG_LEN;
    uint8_t *outer = calloc(1U, outer_length);
    if (outer == NULL) return 0;
    outer[0] = MESSAGE_TRANSPORT_DATA;
    U32TO8_LITTLE(outer + 4U, keypair->remote_index);
    U64TO8_LITTLE(outer + 8U, keypair->sending_counter);
    if (packet_length > 0U) memcpy(outer + 16U, packet, packet_length);
    wireguard_encrypt_packet(outer + 16U, outer + 16U, padded, keypair);
    int sent = ch_wg_send_outer(session, peer, outer, outer_length);
    free(outer);
    if (sent) {
        uint32_t now = wireguard_sys_now();
        peer->protocol->last_tx = now;
        keypair->last_tx = now;
    }
    if (keypair->sending_counter >= REKEY_AFTER_MESSAGES ||
        (keypair->initiator && wireguard_expired(
            keypair->keypair_millis, REKEY_AFTER_TIME))) {
        peer->protocol->send_handshake = true;
    }
    return sent;
}

static void ch_wg_queue_packet(ch_wg_peer *peer, const uint8_t *bytes,
                               size_t length) {
    if (length == 0U || length > CH_WG_PENDING_BYTES ||
        peer->pending_count >= CH_WG_PENDING_PACKETS ||
        peer->pending_bytes > CH_WG_PENDING_BYTES - length) return;
    ch_wg_pending *pending = calloc(1U, sizeof(*pending));
    if (pending == NULL) return;
    pending->bytes = malloc(length);
    if (pending->bytes == NULL) {
        free(pending);
        return;
    }
    memcpy(pending->bytes, bytes, length);
    pending->length = length;
    if (peer->pending_tail == NULL) peer->pending_head = pending;
    else peer->pending_tail->next = pending;
    peer->pending_tail = pending;
    ++peer->pending_count;
    peer->pending_bytes += length;
}

static void ch_wg_flush_pending_locked(ch_wg_session *session,
                                       ch_wg_peer *peer) {
    while (peer->pending_head != NULL) {
        ch_wg_pending *pending = peer->pending_head;
        if (!ch_wg_send_transport_locked(session, peer, pending->bytes,
                                         pending->length)) break;
        peer->pending_head = pending->next;
        if (peer->pending_head == NULL) peer->pending_tail = NULL;
        --peer->pending_count;
        peer->pending_bytes -= pending->length;
        free(pending->bytes);
        free(pending);
    }
}

static void ch_wg_tunnel_write(const uint8_t *packet, size_t length,
                               void *context) {
    ch_wg_session *session = context;
    int family = 0;
    const uint8_t *source = NULL;
    const uint8_t *destination = NULL;
    size_t packet_length = 0U;
    if (session == NULL || !ch_wg_inner_addresses(
            packet, length, &family, &source, &destination,
            &packet_length)) return;
    (void)source;
    (void)pthread_mutex_lock(&session->mutex);
    if (!session->stopping) {
        ch_wg_peer *peer = ch_wg_route_peer(session, family, destination);
        if (peer != NULL && !ch_wg_send_transport_locked(
                session, peer, packet, packet_length)) {
            ch_wg_queue_packet(peer, packet, packet_length);
            peer->protocol->send_handshake = true;
        }
    }
    (void)pthread_mutex_unlock(&session->mutex);
}

static int ch_wg_can_send_initiation(const struct wireguard_peer *peer) {
    return peer->last_initiation_tx == 0U ||
        wireguard_expired(peer->last_initiation_tx, REKEY_TIMEOUT);
}

static int ch_wg_send_handshake_locked(ch_wg_session *session,
                                       ch_wg_peer *peer) {
    if (!ch_wg_can_send_initiation(peer->protocol)) return 0;
    struct message_handshake_initiation message;
    if (!wireguard_create_handshake_initiation(
            &session->device, peer->protocol, &message)) return 0;
    int sent = ch_wg_send_outer(session, peer, &message, sizeof(message));
    if (sent) {
        peer->protocol->send_handshake = false;
        peer->protocol->last_initiation_tx = wireguard_sys_now();
        memcpy(peer->protocol->handshake_mac1, message.mac1,
               WIREGUARD_COOKIE_LEN);
        peer->protocol->handshake_mac1_valid = true;
    }
    crypto_zero(&message, sizeof(message));
    return sent;
}

static int ch_wg_send_response_locked(ch_wg_session *session,
                                      ch_wg_peer *peer) {
    struct message_handshake_response response;
    if (!wireguard_create_handshake_response(
            &session->device, peer->protocol, &response)) return 0;
    int sent = ch_wg_send_outer(session, peer, &response, sizeof(response));
    if (sent) wireguard_start_session(peer->protocol, false);
    crypto_zero(&response, sizeof(response));
    return sent;
}

static void ch_wg_source_bytes(const struct sockaddr_storage *source,
                               uint8_t output[18], size_t *out_length) {
    *out_length = 0U;
    if (source->ss_family == AF_INET) {
        const struct sockaddr_in *value = (const struct sockaddr_in *)source;
        memcpy(output, &value->sin_addr, 4U);
        memcpy(output + 4U, &value->sin_port, 2U);
        *out_length = 6U;
    } else if (source->ss_family == AF_INET6) {
        const struct sockaddr_in6 *value =
            (const struct sockaddr_in6 *)source;
        memcpy(output, &value->sin6_addr, 16U);
        memcpy(output + 16U, &value->sin6_port, 2U);
        *out_length = 18U;
    }
}

static void ch_wg_process_initiation_locked(
    ch_wg_session *session, const uint8_t *bytes, size_t length,
    const struct sockaddr_storage *source, socklen_t source_length) {
    if (length != sizeof(struct message_handshake_initiation)) return;
    struct message_handshake_initiation message;
    memcpy(&message, bytes, sizeof(message));
    if (!wireguard_check_mac1(
            &session->device, (const uint8_t *)&message,
            sizeof(message) - 2U * WIREGUARD_COOKIE_LEN, message.mac1)) {
        crypto_zero(&message, sizeof(message));
        return;
    }
    if (wireguard_is_under_load()) {
        uint8_t source_bytes[18];
        size_t source_bytes_length = 0U;
        ch_wg_source_bytes(source, source_bytes, &source_bytes_length);
        if (!wireguard_check_mac2(
                &session->device, (const uint8_t *)&message,
                sizeof(message) - WIREGUARD_COOKIE_LEN, source_bytes,
                source_bytes_length, message.mac2)) {
            struct message_cookie_reply cookie;
            wireguard_create_cookie_reply(
                &session->device, &cookie, message.mac1, message.sender,
                source_bytes, source_bytes_length);
            int descriptor = source->ss_family == AF_INET6 ?
                session->ipv6_socket : session->ipv4_socket;
            if (descriptor >= 0) {
                (void)sendto(descriptor, &cookie, sizeof(cookie), 0,
                             (const struct sockaddr *)source, source_length);
            }
            crypto_zero(&cookie, sizeof(cookie));
            crypto_zero(&message, sizeof(message));
            return;
        }
    }
    struct wireguard_peer *protocol = wireguard_process_initiation_message(
        &session->device, &message);
    ch_wg_peer *peer = ch_wg_peer_for_protocol(session, protocol);
    if (peer != NULL) {
        ch_wg_update_endpoint(peer, source, source_length);
        (void)ch_wg_send_response_locked(session, peer);
    }
    crypto_zero(&message, sizeof(message));
}

static void ch_wg_process_response_locked(
    ch_wg_session *session, const uint8_t *bytes, size_t length,
    const struct sockaddr_storage *source, socklen_t source_length) {
    if (length != sizeof(struct message_handshake_response)) return;
    struct message_handshake_response response;
    memcpy(&response, bytes, sizeof(response));
    if (!wireguard_check_mac1(
            &session->device, (const uint8_t *)&response,
            sizeof(response) - 2U * WIREGUARD_COOKIE_LEN, response.mac1)) {
        crypto_zero(&response, sizeof(response));
        return;
    }
    uint32_t receiver = U8TO32_LITTLE(bytes + 8U);
    struct wireguard_peer *protocol = peer_lookup_by_handshake(
        &session->device, receiver);
    ch_wg_peer *peer = ch_wg_peer_for_protocol(session, protocol);
    if (peer != NULL && wireguard_process_handshake_response(
            &session->device, protocol, &response)) {
        ch_wg_update_endpoint(peer, source, source_length);
        wireguard_start_session(protocol, true);
        (void)ch_wg_send_transport_locked(session, peer, NULL, 0U);
        ch_wg_flush_pending_locked(session, peer);
    }
    crypto_zero(&response, sizeof(response));
}

static void ch_wg_process_cookie_locked(
    ch_wg_session *session, const uint8_t *bytes, size_t length,
    const struct sockaddr_storage *source, socklen_t source_length) {
    if (length != sizeof(struct message_cookie_reply)) return;
    struct message_cookie_reply cookie;
    memcpy(&cookie, bytes, sizeof(cookie));
    uint32_t receiver = U8TO32_LITTLE(bytes + 4U);
    struct wireguard_peer *protocol = peer_lookup_by_handshake(
        &session->device, receiver);
    ch_wg_peer *peer = ch_wg_peer_for_protocol(session, protocol);
    if (peer != NULL && wireguard_process_cookie_message(
            &session->device, protocol, &cookie)) {
        ch_wg_update_endpoint(peer, source, source_length);
    }
    crypto_zero(&cookie, sizeof(cookie));
}

static uint8_t *ch_wg_process_data_locked(
    ch_wg_session *session, const uint8_t *bytes, size_t length,
    const struct sockaddr_storage *source, socklen_t source_length,
    size_t *out_length) {
    *out_length = 0U;
    if (length < 16U + WIREGUARD_AUTHTAG_LEN) return NULL;
    uint32_t receiver = U8TO32_LITTLE(bytes + 4U);
    uint64_t counter = U8TO64_LITTLE(bytes + 8U);
    struct wireguard_peer *protocol = peer_lookup_by_receiver(
        &session->device, receiver);
    ch_wg_peer *peer = ch_wg_peer_for_protocol(session, protocol);
    if (peer == NULL) return NULL;
    struct wireguard_keypair *keypair = get_peer_keypair_for_idx(
        protocol, receiver);
    if (keypair == NULL || !keypair->receiving_valid ||
        wireguard_expired(keypair->keypair_millis, REJECT_AFTER_TIME) ||
        counter >= REJECT_AFTER_MESSAGES) {
        if (keypair != NULL) keypair_destroy(keypair);
        return NULL;
    }
    size_t encrypted_length = length - 16U;
    size_t plain_capacity = encrypted_length - WIREGUARD_AUTHTAG_LEN;
    uint8_t *plain = plain_capacity == 0U ? NULL : malloc(plain_capacity);
    uint8_t empty = 0U;
    uint8_t *destination = plain == NULL ? &empty : plain;
    if ((plain_capacity > 0U && plain == NULL) ||
        !wireguard_decrypt_packet(destination, bytes + 16U,
                                  encrypted_length, counter, keypair) ||
        !wireguard_check_replay(keypair, counter)) {
        free(plain);
        return NULL;
    }
    int family = 0;
    const uint8_t *inner_source = NULL;
    const uint8_t *inner_destination = NULL;
    size_t packet_length = 0U;
    if (plain_capacity > 0U && (!ch_wg_inner_addresses(
            plain, plain_capacity, &family, &inner_source,
            &inner_destination, &packet_length) ||
        !ch_wg_source_allowed(peer, family, inner_source))) {
        free(plain);
        return NULL;
    }
    (void)inner_destination;
    ch_wg_update_endpoint(peer, source, source_length);
    uint32_t now = wireguard_sys_now();
    keypair->last_rx = now;
    protocol->last_rx = now;
    int was_initiator = keypair->initiator;
    uint32_t key_created = keypair->keypair_millis;
    keypair_update(protocol, keypair);
    if (was_initiator && wireguard_expired(
            key_created, REJECT_AFTER_TIME - REKEY_TIMEOUT)) {
        protocol->send_handshake = true;
    }
    ch_wg_flush_pending_locked(session, peer);
    if (plain_capacity == 0U) {
        free(plain);
        return NULL;
    }
    *out_length = packet_length;
    return plain;
}

static void ch_wg_process_outer(ch_wg_session *session,
                                const uint8_t *bytes, size_t length,
                                const struct sockaddr_storage *source,
                                socklen_t source_length) {
    uint8_t type = wireguard_get_message_type(bytes, length);
    uint8_t *inner = NULL;
    size_t inner_length = 0U;
    (void)pthread_mutex_lock(&session->mutex);
    if (!session->stopping) {
        if (type == MESSAGE_HANDSHAKE_INITIATION) {
            ch_wg_process_initiation_locked(session, bytes, length, source,
                                            source_length);
        } else if (type == MESSAGE_HANDSHAKE_RESPONSE) {
            ch_wg_process_response_locked(session, bytes, length, source,
                                          source_length);
        } else if (type == MESSAGE_COOKIE_REPLY) {
            ch_wg_process_cookie_locked(session, bytes, length, source,
                                        source_length);
        } else if (type == MESSAGE_TRANSPORT_DATA) {
            inner = ch_wg_process_data_locked(
                session, bytes, length, source, source_length,
                &inner_length);
        }
    }
    (void)pthread_mutex_unlock(&session->mutex);
    if (inner != NULL && inner_length > 0U) {
        ch_error ignored;
        (void)ch_tunnel_stack_inject(session->tunnel, inner, inner_length,
                                     &ignored);
    }
    free(inner);
}

static void ch_wg_reset_peer_locked(ch_wg_peer *peer) {
    keypair_destroy(&peer->protocol->next_keypair);
    keypair_destroy(&peer->protocol->curr_keypair);
    keypair_destroy(&peer->protocol->prev_keypair);
    memset(&peer->protocol->handshake, 0,
           sizeof(peer->protocol->handshake));
    peer->endpoint = peer->configured_endpoint;
    peer->endpoint_length = peer->configured_endpoint_length;
    peer->protocol->send_handshake = true;
}

static void ch_wg_timer_locked(ch_wg_session *session) {
    for (size_t index = 0U; index < session->peer_count; ++index) {
        ch_wg_peer *peer = &session->peers[index];
        struct wireguard_peer *protocol = peer->protocol;
        if (protocol->curr_keypair.valid && wireguard_expired(
                protocol->curr_keypair.keypair_millis,
                REJECT_AFTER_TIME * 3U)) {
            ch_wg_reset_peer_locked(peer);
        }
        if (protocol->curr_keypair.valid &&
            (wireguard_expired(protocol->curr_keypair.keypair_millis,
                               REJECT_AFTER_TIME) ||
             protocol->curr_keypair.sending_counter >=
                 REJECT_AFTER_MESSAGES)) {
            keypair_destroy(&protocol->curr_keypair);
        }
        if (protocol->keepalive_interval > 0U &&
            ch_wg_sending_key(peer) != NULL &&
            wireguard_expired(protocol->last_tx,
                              protocol->keepalive_interval)) {
            (void)ch_wg_send_transport_locked(session, peer, NULL, 0U);
        }
        int need_handshake = protocol->send_handshake ||
            (!protocol->curr_keypair.valid && protocol->active) ||
            peer->pending_head != NULL;
        if (protocol->curr_keypair.valid &&
            protocol->curr_keypair.initiator &&
            wireguard_expired(protocol->curr_keypair.keypair_millis,
                              REKEY_AFTER_TIME)) {
            need_handshake = 1;
        }
        if (need_handshake) (void)ch_wg_send_handshake_locked(session, peer);
        if (ch_wg_sending_key(peer) != NULL) {
            ch_wg_flush_pending_locked(session, peer);
        }
    }
}

static void ch_wg_drain_wake(ch_wg_session *session) {
    uint8_t bytes[64];
    while (read(session->wake_read, bytes, sizeof(bytes)) > 0) {
    }
}

static void ch_wg_receive_socket(ch_wg_session *session, int descriptor,
                                 uint8_t *buffer, size_t capacity) {
    for (;;) {
        struct sockaddr_storage source;
        socklen_t source_length = (socklen_t)sizeof(source);
        ssize_t length = recvfrom(descriptor, buffer, capacity, 0,
                                  (struct sockaddr *)&source,
                                  &source_length);
        if (length < 0 && errno == EINTR) continue;
        if (length < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) return;
        if (length <= 0) return;
        ch_wg_process_outer(session, buffer, (size_t)length, &source,
                            source_length);
    }
}

static void *ch_wg_worker_main(void *opaque) {
    ch_wg_session *session = opaque;
    uint8_t buffer[65536U];
    for (;;) {
        struct pollfd descriptors[3];
        nfds_t count = 0U;
        descriptors[count++] = (struct pollfd){
            .fd = session->wake_read, .events = POLLIN};
        if (session->ipv4_socket >= 0) {
            descriptors[count++] = (struct pollfd){
                .fd = session->ipv4_socket, .events = POLLIN};
        }
        if (session->ipv6_socket >= 0) {
            descriptors[count++] = (struct pollfd){
                .fd = session->ipv6_socket, .events = POLLIN};
        }
        int ready;
        do {
            ready = poll(descriptors, count, CH_WG_POLL_MILLISECONDS);
        } while (ready < 0 && errno == EINTR);
        if (ready > 0) {
            for (nfds_t index = 0U; index < count; ++index) {
                if ((descriptors[index].revents & POLLIN) == 0) continue;
                if (descriptors[index].fd == session->wake_read) {
                    ch_wg_drain_wake(session);
                } else {
                    ch_wg_receive_socket(session, descriptors[index].fd,
                                         buffer, sizeof(buffer));
                }
            }
        }
        (void)pthread_mutex_lock(&session->mutex);
        int stopping = session->stopping;
        if (!stopping) ch_wg_timer_locked(session);
        (void)pthread_mutex_unlock(&session->mutex);
        if (stopping) break;
    }
    crypto_zero(buffer, sizeof(buffer));
    return NULL;
}

static void ch_wg_session_destroy(ch_wg_session *session) {
    if (session == NULL) return;
    if (session->worker_started) {
        (void)pthread_mutex_lock(&session->mutex);
        session->stopping = 1;
        uint8_t wake = 1U;
        (void)write(session->wake_write, &wake, sizeof(wake));
        (void)pthread_mutex_unlock(&session->mutex);
        (void)pthread_join(session->worker, NULL);
    }
    ch_tunnel_stack_destroy(session->tunnel);
    session->tunnel = NULL;
    ch_wg_close_descriptor(&session->ipv4_socket);
    ch_wg_close_descriptor(&session->ipv6_socket);
    ch_wg_close_descriptor(&session->wake_read);
    ch_wg_close_descriptor(&session->wake_write);
    for (size_t index = 0U; index < session->peer_count; ++index) {
        ch_wg_pending_clear(&session->peers[index]);
    }
    ch_wg_free_strings(session->addresses, session->address_count);
    ch_wg_free_strings(session->dns_servers, session->dns_server_count);
    crypto_zero(&session->device, sizeof(session->device));
    (void)pthread_mutex_destroy(&session->mutex);
    free(session->key);
    free(session);
}

static ch_wg_session *ch_wg_session_create(const ch_config_table *server,
                                           char *key,
                                           ch_error *error) {
    ch_wg_session *session = calloc(1U, sizeof(*session));
    if (session == NULL) {
        free(key);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "wireguard: allocate session");
        return NULL;
    }
    session->key = key;
    session->ipv4_socket = -1;
    session->ipv6_socket = -1;
    session->wake_read = -1;
    session->wake_write = -1;
    if (pthread_mutex_init(&session->mutex, NULL) != 0) {
        free(session->key);
        free(session);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "wireguard: initialize session lock");
        return NULL;
    }
    if (!ch_wg_configure_session(session, server, error)) {
        ch_wg_session_destroy(session);
        return NULL;
    }
    int wake[2] = {-1, -1};
    if (pipe(wake) != 0 || !ch_wg_set_nonblocking(wake[0]) ||
        !ch_wg_set_nonblocking(wake[1])) {
        ch_wg_close_descriptor(&wake[0]);
        ch_wg_close_descriptor(&wake[1]);
        ch_error_set(error, CH_ERROR_IO,
                     "wireguard: initialize worker wake pipe");
        ch_wg_session_destroy(session);
        return NULL;
    }
    session->wake_read = wake[0];
    session->wake_write = wake[1];
    ch_tunnel_stack_options options = {
        .addresses = (const char *const *)session->addresses,
        .address_count = session->address_count,
        .dns_servers = (const char *const *)session->dns_servers,
        .dns_server_count = session->dns_server_count,
        .mtu = session->mtu,
        .packet_writer = ch_wg_tunnel_write,
        .packet_writer_context = session
    };
    session->tunnel = ch_tunnel_stack_create(&options, error);
    if (session->tunnel == NULL) {
        ch_wg_session_destroy(session);
        return NULL;
    }
    if (pthread_create(&session->worker, NULL, ch_wg_worker_main,
                       session) != 0) {
        ch_error_set(error, CH_ERROR_IO,
                     "wireguard: start protocol worker");
        ch_wg_session_destroy(session);
        return NULL;
    }
    session->worker_started = 1;
    return session;
}

static ch_wg_session *ch_wg_session_for_server(
    const ch_config_table *server, ch_error *error) {
    char *key = NULL;
    ch_status encoded = ch_config_table_json(server, &key, error);
    if (encoded != CH_OK) return NULL;
    (void)pthread_mutex_lock(&ch_wg_registry_mutex);
    for (ch_wg_session *session = ch_wg_registry; session != NULL;
         session = session->next) {
        if (strcmp(session->key, key) == 0) {
            (void)pthread_mutex_unlock(&ch_wg_registry_mutex);
            free(key);
            return session;
        }
    }
    ch_wg_session *session = ch_wg_session_create(server, key, error);
    if (session != NULL) {
        session->next = ch_wg_registry;
        ch_wg_registry = session;
    }
    (void)pthread_mutex_unlock(&ch_wg_registry_mutex);
    return session;
}

ch_status ch_protocol_wireguard_dial(const ch_config_table *server,
                                     const char *target,
                                     int *out_descriptor,
                                     ch_error *error) {
    ch_error_clear(error);
    if (server == NULL || target == NULL || out_descriptor == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: server, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    ch_wg_session *session = ch_wg_session_for_server(server, error);
    if (session == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                               error->code;
    return ch_tunnel_stack_dial_tcp(session->tunnel, target,
                                    out_descriptor, error);
}

ch_status ch_protocol_wireguard_open_packet(
    const ch_config_table *server,
    ch_tunnel_packet **out_packet,
    ch_error *error) {
    ch_error_clear(error);
    if (server == NULL || out_packet == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "wireguard: server and packet output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_packet = NULL;
    ch_wg_session *session = ch_wg_session_for_server(server, error);
    if (session == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                               error->code;
    return ch_tunnel_stack_open_packet(session->tunnel, out_packet, error);
}

void ch_protocol_wireguard_reset(void) {
    (void)pthread_mutex_lock(&ch_wg_registry_mutex);
    ch_wg_session *sessions = ch_wg_registry;
    ch_wg_registry = NULL;
    (void)pthread_mutex_unlock(&ch_wg_registry_mutex);
    while (sessions != NULL) {
        ch_wg_session *next = sessions->next;
        sessions->next = NULL;
        ch_wg_session_destroy(sessions);
        sessions = next;
    }
}
