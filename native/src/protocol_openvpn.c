#include "protocol_openvpn.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <netdb.h>
#include <openssl/err.h>
#include <openssl/pem.h>
#include <openssl/rand.h>
#include <openssl/ssl.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "cnet.h"
#include "internal.h"

#define CH_OVPN_OPCODE_CONTROL_V1 4U
#define CH_OVPN_OPCODE_ACK_V1 5U
#define CH_OVPN_OPCODE_HARD_RESET_CLIENT_V2 7U
#define CH_OVPN_OPCODE_HARD_RESET_SERVER_V2 8U
#define CH_OVPN_OPCODE_DATA_V2 9U
#define CH_OVPN_SESSION_ID_LENGTH 8U
#define CH_OVPN_CONTROL_FRAGMENT 1200U
#define CH_OVPN_REORDER_WINDOW 64U
#define CH_OVPN_MAX_PENDING_ACKS 256U
#define CH_OVPN_MAX_CONTROL_PACKET 65535U
#define CH_OVPN_HANDSHAKE_MILLISECONDS 60000U
#define CH_OVPN_RESET_MILLISECONDS 30000U
#define CH_OVPN_DATA_REKEY_THRESHOLD UINT32_C(0xff000000)
#define CH_OVPN_KEY_BLOCK_SIZE 256U
#define CH_OVPN_SLOT_SIZE 64U
#define CH_OVPN_TICK_MILLISECONDS 250

typedef enum ch_ovpn_cipher {
    CH_OVPN_CIPHER_AES_256_GCM = 1,
    CH_OVPN_CIPHER_CHACHA20_POLY1305 = 2
} ch_ovpn_cipher;

typedef struct ch_ovpn_config {
    char *remote;
    char *ca_cert;
    char *client_cert;
    char *client_key;
    char *server_cn;
    char *username;
    char *password;
    int skip_verify;
    ch_ovpn_cipher pinned_cipher;
    unsigned int tun_mtu;
} ch_ovpn_config;

typedef struct ch_ovpn_outstanding {
    struct ch_ovpn_outstanding *next;
    uint32_t id;
    uint8_t *bytes;
    size_t length;
    uint64_t sent_at;
    unsigned int retries;
} ch_ovpn_outstanding;

typedef struct ch_ovpn_reorder {
    struct ch_ovpn_reorder *next;
    uint32_t id;
    uint8_t opcode;
    uint8_t *payload;
    size_t payload_length;
} ch_ovpn_reorder;

typedef struct ch_ovpn_data {
    ch_ovpn_cipher cipher;
    uint8_t key_id;
    uint32_t peer_id;
    uint8_t send_key[32];
    uint8_t send_iv[8];
    uint8_t receive_key[32];
    uint8_t receive_iv[8];
    uint32_t next_packet_id;
    uint32_t highest_packet_id;
    uint64_t replay_window;
} ch_ovpn_data;

typedef struct ch_ovpn_session {
    struct ch_ovpn_session *next;
    char *key;
    ch_ovpn_config config;
    int socket;
    int wake_read;
    int wake_write;
    uint8_t local_session[CH_OVPN_SESSION_ID_LENGTH];
    uint8_t remote_session[CH_OVPN_SESSION_ID_LENGTH];
    int have_remote_session;
    uint8_t key_id;
    uint32_t next_control_id;
    uint32_t next_expected_id;
    ch_ovpn_outstanding *outstanding;
    uint32_t pending_acks[CH_OVPN_MAX_PENDING_ACKS];
    size_t pending_ack_count;
    ch_ovpn_reorder *reorder;
    uint8_t *control_stream;
    size_t control_stream_offset;
    size_t control_stream_length;
    int saw_server_reset;
    uint64_t handshake_deadline;
    SSL_CTX *tls_context;
    ch_ovpn_data data;
    int data_ready;
    char **addresses;
    size_t address_count;
    char **dns_servers;
    size_t dns_server_count;
    unsigned int mtu;
    ch_tunnel_stack *tunnel;
    pthread_mutex_t mutex;
    pthread_t worker;
    int worker_started;
    int stopping;
    int failed;
} ch_ovpn_session;

static pthread_mutex_t ch_ovpn_registry_mutex = PTHREAD_MUTEX_INITIALIZER;
static ch_ovpn_session *ch_ovpn_registry;
static pthread_once_t ch_ovpn_bio_once = PTHREAD_ONCE_INIT;
static BIO_METHOD *ch_ovpn_bio_method;

static uint64_t ch_ovpn_now_milliseconds(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) return 0U;
    return (uint64_t)now.tv_sec * UINT64_C(1000) +
        (uint64_t)now.tv_nsec / UINT64_C(1000000);
}

static uint32_t ch_ovpn_read_u32(const uint8_t *bytes) {
    return ((uint32_t)bytes[0] << 24U) |
        ((uint32_t)bytes[1] << 16U) |
        ((uint32_t)bytes[2] << 8U) | bytes[3];
}

static void ch_ovpn_write_u16(uint8_t *bytes, uint16_t value) {
    bytes[0] = (uint8_t)(value >> 8U);
    bytes[1] = (uint8_t)value;
}

static void ch_ovpn_write_u32(uint8_t *bytes, uint32_t value) {
    bytes[0] = (uint8_t)(value >> 24U);
    bytes[1] = (uint8_t)(value >> 16U);
    bytes[2] = (uint8_t)(value >> 8U);
    bytes[3] = (uint8_t)value;
}

static char *ch_ovpn_optional_string(const ch_config_table *table,
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

static int ch_ovpn_nonempty(const char *value) {
    if (value == NULL) return 0;
    while (*value != '\0') {
        if (!isspace((unsigned char)*value)) return 1;
        ++value;
    }
    return 0;
}

static int ch_ovpn_endpoint_shape_valid(const char *remote) {
    if (!ch_ovpn_nonempty(remote)) return 0;
    if (remote[0] == '[') {
        const char *closing = strchr(remote + 1U, ']');
        return closing != NULL && closing != remote + 1U &&
            closing[1] == ':' && closing[2] != '\0';
    }
    const char *separator = strrchr(remote, ':');
    return separator != NULL && separator != remote &&
        separator[1] != '\0' && strchr(remote, ':') == separator;
}

static ch_ovpn_cipher ch_ovpn_cipher_from_name(const char *name) {
    if (name == NULL || name[0] == '\0') return 0;
    if (strcasecmp(name, "AES-256-GCM") == 0) {
        return CH_OVPN_CIPHER_AES_256_GCM;
    }
    if (strcasecmp(name, "CHACHA20-POLY1305") == 0) {
        return CH_OVPN_CIPHER_CHACHA20_POLY1305;
    }
    return 0;
}

static void ch_ovpn_config_free(ch_ovpn_config *config) {
    if (config == NULL) return;
    free(config->remote);
    free(config->ca_cert);
    free(config->client_cert);
    free(config->client_key);
    free(config->server_cn);
    free(config->username);
    free(config->password);
    memset(config, 0, sizeof(*config));
}

static int ch_ovpn_parse_config(const ch_config_table *server,
                                ch_ovpn_config *config,
                                ch_error *error) {
    memset(config, 0, sizeof(*config));
    config->tun_mtu = 1500U;
    config->remote = ch_ovpn_optional_string(server, "address");
    if (!ch_ovpn_nonempty(config->remote)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: address is required (VPN server host:port)");
        return 0;
    }
    if (!ch_ovpn_endpoint_shape_valid(config->remote)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: invalid address %s (expected host:port)",
                     config->remote);
        return 0;
    }
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    config->ca_cert = ch_ovpn_optional_string(settings, "ca_cert");
    config->client_cert = ch_ovpn_optional_string(settings, "client_cert");
    config->client_key = ch_ovpn_optional_string(settings, "client_key");
    config->server_cn = ch_ovpn_optional_string(settings, "server_cn");
    config->username = ch_ovpn_optional_string(settings, "username");
    config->password = ch_ovpn_optional_string(settings, "password");
    if (!ch_ovpn_nonempty(config->ca_cert)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: ca_cert is required (PEM server CA)");
        return 0;
    }
    if (!ch_ovpn_nonempty(config->client_cert) ||
        !ch_ovpn_nonempty(config->client_key)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: client_cert and client_key are required "
                     "(PEM)");
        return 0;
    }
    int have_username = ch_ovpn_nonempty(config->username);
    int have_password = ch_ovpn_nonempty(config->password);
    if (have_username != have_password) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: username and password must be set together");
        return 0;
    }
    if (ch_config_table_has(settings, "skip_cert_verify")) {
        bool value = false;
        if (ch_config_table_get_bool(settings, "skip_cert_verify", &value,
                                     error) != CH_OK) return 0;
        config->skip_verify = value ? 1 : 0;
    }
    char *cipher = ch_ovpn_optional_string(settings, "cipher");
    if (cipher != NULL && cipher[0] != '\0') {
        config->pinned_cipher = ch_ovpn_cipher_from_name(cipher);
        if (config->pinned_cipher == 0) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "openvpn: unsupported cipher %s (supported: "
                         "AES-256-GCM, CHACHA20-POLY1305)", cipher);
            free(cipher);
            return 0;
        }
    }
    free(cipher);
    if (ch_config_table_has(settings, "tun_mtu")) {
        int64_t mtu = 0;
        if (ch_config_table_get_int(settings, "tun_mtu", &mtu, error) !=
                CH_OK || mtu < 1280 || mtu > UINT16_MAX) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "openvpn: tun_mtu must be between 1280 and 65535");
            return 0;
        }
        config->tun_mtu = (unsigned int)mtu;
    }
    return 1;
}

static SSL_CTX *ch_ovpn_tls_context(const ch_ovpn_config *config,
                                    ch_error *error) {
    SSL_CTX *context = SSL_CTX_new(TLS_client_method());
    if (context == NULL || SSL_CTX_set_min_proto_version(
            context, TLS1_2_VERSION) != 1) {
        SSL_CTX_free(context);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "openvpn: initialize TLS 1.2 client");
        return NULL;
    }
    X509_STORE *store = SSL_CTX_get_cert_store(context);
    BIO *ca = BIO_new_mem_buf(config->ca_cert, -1);
    unsigned int ca_count = 0U;
    if (ca != NULL) {
        for (;;) {
            X509 *certificate = PEM_read_bio_X509(ca, NULL, NULL, NULL);
            if (certificate == NULL) break;
            if (X509_STORE_add_cert(store, certificate) == 1) ++ca_count;
            X509_free(certificate);
        }
    }
    BIO_free(ca);
    ERR_clear_error();
    if (ca_count == 0U) {
        SSL_CTX_free(context);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: ca_cert did not contain a valid PEM "
                     "certificate");
        return NULL;
    }
    BIO *certificates = BIO_new_mem_buf(config->client_cert, -1);
    X509 *leaf = certificates == NULL ? NULL :
        PEM_read_bio_X509(certificates, NULL, NULL, NULL);
    int certificate_ok = leaf != NULL &&
        SSL_CTX_use_certificate(context, leaf) == 1;
    X509_free(leaf);
    if (certificate_ok) {
        for (;;) {
            X509 *chain = PEM_read_bio_X509(
                certificates, NULL, NULL, NULL);
            if (chain == NULL) break;
            if (SSL_CTX_add_extra_chain_cert(context, chain) != 1) {
                X509_free(chain);
                certificate_ok = 0;
                break;
            }
        }
    }
    BIO_free(certificates);
    ERR_clear_error();
    BIO *private_pem = BIO_new_mem_buf(config->client_key, -1);
    EVP_PKEY *private_key = private_pem == NULL ? NULL :
        PEM_read_bio_PrivateKey(private_pem, NULL, NULL, NULL);
    BIO_free(private_pem);
    int key_ok = private_key != NULL &&
        SSL_CTX_use_PrivateKey(context, private_key) == 1 &&
        SSL_CTX_check_private_key(context) == 1;
    EVP_PKEY_free(private_key);
    if (!certificate_ok || !key_ok) {
        SSL_CTX_free(context);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: load client certificate/private key");
        return NULL;
    }
    SSL_CTX_set_verify(context, config->skip_verify ? SSL_VERIFY_NONE :
                                                       SSL_VERIFY_PEER,
                       NULL);
    return context;
}

static void ch_ovpn_close_descriptor(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static int ch_ovpn_set_nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL, 0);
    return flags >= 0 && fcntl(descriptor, F_SETFL,
                               flags | O_NONBLOCK) == 0;
}

static int ch_ovpn_connect_udp(const char *remote, ch_error *error) {
    const char *host_start = remote;
    const char *host_end = NULL;
    const char *service = NULL;
    if (remote[0] == '[') {
        host_start = remote + 1U;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            goto invalid;
        }
        service = host_end + 2U;
    } else {
        host_end = strrchr(remote, ':');
        if (host_end == NULL || host_end == remote || host_end[1] == '\0' ||
            strchr(remote, ':') != host_end) goto invalid;
        service = host_end + 1U;
    }
    size_t host_length = (size_t)(host_end - host_start);
    char *host = malloc(host_length + 1U);
    if (host == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "openvpn: copy remote endpoint");
        return -1;
    }
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_DGRAM;
    hints.ai_protocol = IPPROTO_UDP;
    struct addrinfo *addresses = NULL;
    int resolved = getaddrinfo(host, service, &hints, &addresses);
    free(host);
    if (resolved != 0) {
        ch_error_set(error, CH_ERROR_IO, "openvpn: resolve %s: %s",
                     remote, gai_strerror(resolved));
        return -1;
    }
    int descriptor = -1;
    int saved_error = EHOSTUNREACH;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        descriptor = socket(candidate->ai_family, SOCK_DGRAM, IPPROTO_UDP);
        if (descriptor < 0) {
            saved_error = errno;
            continue;
        }
        if (connect(descriptor, candidate->ai_addr,
                    candidate->ai_addrlen) == 0 &&
            ch_ovpn_set_nonblocking(descriptor)) break;
        saved_error = errno;
        ch_ovpn_close_descriptor(&descriptor);
    }
    freeaddrinfo(addresses);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "openvpn: connect UDP %s: %s",
                     remote, strerror(saved_error));
    }
    return descriptor;

invalid:
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                 "openvpn: invalid address %s", remote);
    return -1;
}

static int ch_ovpn_send_raw(ch_ovpn_session *session,
                            const uint8_t *bytes, size_t length) {
    ssize_t written;
    do {
        written = send(session->socket, bytes, length, 0);
    } while (written < 0 && errno == EINTR);
    return written == (ssize_t)length;
}

static void ch_ovpn_remove_outstanding(ch_ovpn_session *session,
                                       uint32_t id) {
    ch_ovpn_outstanding **cursor = &session->outstanding;
    while (*cursor != NULL) {
        ch_ovpn_outstanding *value = *cursor;
        if (value->id == id) {
            *cursor = value->next;
            free(value->bytes);
            free(value);
            return;
        }
        cursor = &value->next;
    }
}

static int ch_ovpn_control_encode(ch_ovpn_session *session,
                                  uint8_t opcode,
                                  const uint8_t *payload,
                                  size_t payload_length,
                                  int track,
                                  uint8_t **out_bytes,
                                  size_t *out_length) {
    size_t ack_count = session->pending_ack_count;
    if (ack_count > 4U) ack_count = 4U;
    size_t length = 1U + CH_OVPN_SESSION_ID_LENGTH + 1U +
        ack_count * 4U + (ack_count > 0U ? CH_OVPN_SESSION_ID_LENGTH : 0U) +
        (opcode == CH_OVPN_OPCODE_ACK_V1 ? 0U : 4U + payload_length);
    uint8_t *bytes = malloc(length);
    if (bytes == NULL) return 0;
    size_t offset = 0U;
    bytes[offset++] = (uint8_t)(((unsigned int)opcode << 3U) |
                                ((unsigned int)session->key_id & 7U));
    memcpy(bytes + offset, session->local_session,
           CH_OVPN_SESSION_ID_LENGTH);
    offset += CH_OVPN_SESSION_ID_LENGTH;
    bytes[offset++] = (uint8_t)ack_count;
    for (size_t index = 0U; index < ack_count; ++index) {
        ch_ovpn_write_u32(bytes + offset, session->pending_acks[index]);
        offset += 4U;
    }
    if (ack_count > 0U) {
        memcpy(bytes + offset, session->remote_session,
               CH_OVPN_SESSION_ID_LENGTH);
        offset += CH_OVPN_SESSION_ID_LENGTH;
        memmove(session->pending_acks,
                session->pending_acks + ack_count,
                (session->pending_ack_count - ack_count) *
                    sizeof(session->pending_acks[0]));
        session->pending_ack_count -= ack_count;
    }
    if (opcode != CH_OVPN_OPCODE_ACK_V1) {
        uint32_t id = session->next_control_id++;
        ch_ovpn_write_u32(bytes + offset, id);
        offset += 4U;
        if (payload_length > 0U) {
            memcpy(bytes + offset, payload, payload_length);
            offset += payload_length;
        }
        if (track) {
            ch_ovpn_outstanding *pending = calloc(1U, sizeof(*pending));
            if (pending == NULL) {
                free(bytes);
                return 0;
            }
            pending->bytes = malloc(length);
            if (pending->bytes == NULL) {
                free(pending);
                free(bytes);
                return 0;
            }
            memcpy(pending->bytes, bytes, length);
            pending->length = length;
            pending->id = id;
            pending->sent_at = ch_ovpn_now_milliseconds();
            pending->next = session->outstanding;
            session->outstanding = pending;
        }
    }
    *out_bytes = bytes;
    *out_length = offset;
    return offset == length;
}

static int ch_ovpn_send_control(ch_ovpn_session *session, uint8_t opcode,
                                const uint8_t *payload,
                                size_t payload_length) {
    uint8_t *bytes = NULL;
    size_t length = 0U;
    if (!ch_ovpn_control_encode(session, opcode, payload, payload_length,
                                1, &bytes, &length)) return 0;
    int sent = ch_ovpn_send_raw(session, bytes, length);
    free(bytes);
    return sent;
}

static int ch_ovpn_send_ack(ch_ovpn_session *session) {
    if (!session->have_remote_session || session->pending_ack_count == 0U) {
        return 1;
    }
    uint8_t *bytes = NULL;
    size_t length = 0U;
    if (!ch_ovpn_control_encode(session, CH_OVPN_OPCODE_ACK_V1, NULL, 0U,
                                0, &bytes, &length)) return 0;
    int sent = ch_ovpn_send_raw(session, bytes, length);
    free(bytes);
    return sent;
}

static int ch_ovpn_stream_append(ch_ovpn_session *session,
                                 const uint8_t *bytes, size_t length) {
    if (length == 0U) return 1;
    if (session->control_stream_offset > 0U &&
        session->control_stream_length > 0U) {
        memmove(session->control_stream,
                session->control_stream + session->control_stream_offset,
                session->control_stream_length);
        session->control_stream_offset = 0U;
    }
    uint8_t *next = realloc(session->control_stream,
                            session->control_stream_length + length);
    if (next == NULL) return 0;
    memcpy(next + session->control_stream_length, bytes, length);
    session->control_stream = next;
    session->control_stream_length += length;
    return 1;
}

static ch_ovpn_reorder *ch_ovpn_reorder_take(ch_ovpn_session *session,
                                             uint32_t id) {
    ch_ovpn_reorder **cursor = &session->reorder;
    while (*cursor != NULL) {
        ch_ovpn_reorder *value = *cursor;
        if (value->id == id) {
            *cursor = value->next;
            value->next = NULL;
            return value;
        }
        cursor = &value->next;
    }
    return NULL;
}

static int ch_ovpn_reorder_contains(ch_ovpn_session *session, uint32_t id) {
    for (ch_ovpn_reorder *value = session->reorder; value != NULL;
         value = value->next) {
        if (value->id == id) return 1;
    }
    return 0;
}

static int ch_ovpn_handle_control(ch_ovpn_session *session,
                                  const uint8_t *bytes, size_t length) {
    if (length < 10U) return 0;
    uint8_t opcode = bytes[0] >> 3U;
    uint8_t key_id = bytes[0] & 7U;
    if (opcode != CH_OVPN_OPCODE_CONTROL_V1 &&
        opcode != CH_OVPN_OPCODE_ACK_V1 &&
        opcode != CH_OVPN_OPCODE_HARD_RESET_CLIENT_V2 &&
        opcode != CH_OVPN_OPCODE_HARD_RESET_SERVER_V2) return 0;
    if (key_id != session->key_id) return 0;
    size_t offset = 1U;
    const uint8_t *remote_session = bytes + offset;
    offset += CH_OVPN_SESSION_ID_LENGTH;
    size_t ack_count = bytes[offset++];
    if (ack_count > (length - offset) / 4U) return 0;
    const uint8_t *ack_bytes = bytes + offset;
    offset += ack_count * 4U;
    if (session->have_remote_session && memcmp(
            session->remote_session, remote_session,
            CH_OVPN_SESSION_ID_LENGTH) != 0) return 0;
    if (ack_count > 0U) {
        if (offset + CH_OVPN_SESSION_ID_LENGTH > length) return 0;
        if (memcmp(bytes + offset, session->local_session,
                   CH_OVPN_SESSION_ID_LENGTH) != 0) return 0;
        offset += CH_OVPN_SESSION_ID_LENGTH;
    }
    if (!session->have_remote_session) {
        memcpy(session->remote_session, remote_session,
               CH_OVPN_SESSION_ID_LENGTH);
        session->have_remote_session = 1;
    }
    for (size_t index = 0U; index < ack_count; ++index) {
        ch_ovpn_remove_outstanding(
            session, ch_ovpn_read_u32(ack_bytes + index * 4U));
    }
    if (opcode == CH_OVPN_OPCODE_ACK_V1) return offset == length;
    if (offset + 4U > length) return 0;
    uint32_t id = ch_ovpn_read_u32(bytes + offset);
    offset += 4U;
    if (session->pending_ack_count < CH_OVPN_MAX_PENDING_ACKS) {
        session->pending_acks[session->pending_ack_count++] = id;
    }
    if (id < session->next_expected_id ||
        id - session->next_expected_id >= CH_OVPN_REORDER_WINDOW ||
        ch_ovpn_reorder_contains(session, id)) {
        (void)ch_ovpn_send_ack(session);
        return 1;
    }
    ch_ovpn_reorder *packet = calloc(1U, sizeof(*packet));
    if (packet == NULL) return 0;
    packet->id = id;
    packet->opcode = opcode;
    packet->payload_length = length - offset;
    if (packet->payload_length > 0U) {
        packet->payload = malloc(packet->payload_length);
        if (packet->payload == NULL) {
            free(packet);
            return 0;
        }
        memcpy(packet->payload, bytes + offset, packet->payload_length);
    }
    packet->next = session->reorder;
    session->reorder = packet;
    for (;;) {
        ch_ovpn_reorder *next = ch_ovpn_reorder_take(
            session, session->next_expected_id);
        if (next == NULL) break;
        if (next->opcode == CH_OVPN_OPCODE_HARD_RESET_SERVER_V2) {
            session->saw_server_reset = 1;
        } else if (next->opcode == CH_OVPN_OPCODE_CONTROL_V1 &&
                   next->payload_length > 0U &&
                   !ch_ovpn_stream_append(session, next->payload,
                                          next->payload_length)) {
            free(next->payload);
            free(next);
            return 0;
        }
        ++session->next_expected_id;
        free(next->payload);
        free(next);
    }
    return ch_ovpn_send_ack(session);
}

static int ch_ovpn_retransmit(ch_ovpn_session *session) {
    uint64_t now = ch_ovpn_now_milliseconds();
    for (ch_ovpn_outstanding *pending = session->outstanding;
         pending != NULL; pending = pending->next) {
        uint64_t interval = UINT64_C(1000) << pending->retries;
        if (interval > UINT64_C(16000)) interval = UINT64_C(16000);
        if (now - pending->sent_at < interval) continue;
        if (pending->retries >= 10U || !ch_ovpn_send_raw(
                session, pending->bytes, pending->length)) return 0;
        pending->sent_at = now;
        ++pending->retries;
    }
    return 1;
}

static int ch_ovpn_pump(ch_ovpn_session *session, uint64_t deadline,
                        int want_reset, int want_stream) {
    for (;;) {
        if ((want_reset && session->saw_server_reset) ||
            (want_stream && session->control_stream_length > 0U)) return 1;
        uint64_t now = ch_ovpn_now_milliseconds();
        if (now >= deadline) {
            errno = ETIMEDOUT;
            return 0;
        }
        int timeout = CH_OVPN_TICK_MILLISECONDS;
        uint64_t remaining = deadline - now;
        if (remaining < (uint64_t)timeout) timeout = (int)remaining;
        struct pollfd wait = {.fd = session->socket, .events = POLLIN};
        int ready;
        do {
            ready = poll(&wait, 1U, timeout);
        } while (ready < 0 && errno == EINTR);
        if (ready < 0 || !ch_ovpn_retransmit(session)) return 0;
        if (ready == 0 || (wait.revents & POLLIN) == 0) continue;
        for (;;) {
            uint8_t packet[CH_OVPN_MAX_CONTROL_PACKET];
            ssize_t length = recv(session->socket, packet, sizeof(packet), 0);
            if (length < 0 && errno == EINTR) continue;
            if (length < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
                break;
            }
            if (length <= 0) return 0;
            uint8_t opcode = packet[0] >> 3U;
            if (opcode != CH_OVPN_OPCODE_DATA_V2 &&
                !ch_ovpn_handle_control(session, packet, (size_t)length)) {
                continue;
            }
        }
    }
}

static int ch_ovpn_bio_create(BIO *bio) {
    BIO_set_init(bio, 1);
    BIO_set_data(bio, NULL);
    BIO_set_shutdown(bio, 0);
    return 1;
}

static int ch_ovpn_bio_destroy(BIO *bio) {
    if (bio == NULL) return 0;
    BIO_set_data(bio, NULL);
    BIO_set_init(bio, 0);
    return 1;
}

static int ch_ovpn_bio_read(BIO *bio, char *output, int capacity) {
    ch_ovpn_session *session = BIO_get_data(bio);
    if (session == NULL || output == NULL || capacity <= 0) return 0;
    BIO_clear_retry_flags(bio);
    if (session->control_stream_length == 0U && !ch_ovpn_pump(
            session, session->handshake_deadline, 0, 1)) return -1;
    size_t amount = session->control_stream_length;
    if (amount > (size_t)capacity) amount = (size_t)capacity;
    memcpy(output, session->control_stream + session->control_stream_offset,
           amount);
    session->control_stream_offset += amount;
    session->control_stream_length -= amount;
    if (session->control_stream_length == 0U) {
        free(session->control_stream);
        session->control_stream = NULL;
        session->control_stream_offset = 0U;
    }
    return (int)amount;
}

static int ch_ovpn_bio_write(BIO *bio, const char *input, int length) {
    ch_ovpn_session *session = BIO_get_data(bio);
    if (session == NULL || input == NULL || length <= 0) return 0;
    BIO_clear_retry_flags(bio);
    size_t offset = 0U;
    while (offset < (size_t)length) {
        size_t amount = (size_t)length - offset;
        if (amount > CH_OVPN_CONTROL_FRAGMENT) {
            amount = CH_OVPN_CONTROL_FRAGMENT;
        }
        if (!ch_ovpn_send_control(
                session, CH_OVPN_OPCODE_CONTROL_V1,
                (const uint8_t *)input + offset, amount)) {
            return offset == 0U ? -1 : (int)offset;
        }
        offset += amount;
    }
    return length;
}

static long ch_ovpn_bio_control(BIO *bio, int command, long argument,
                                void *pointer) {
    (void)bio;
    (void)argument;
    (void)pointer;
    return command == BIO_CTRL_FLUSH ? 1L : 0L;
}

static void ch_ovpn_initialize_bio(void) {
    int type = BIO_get_new_index() | BIO_TYPE_SOURCE_SINK;
    ch_ovpn_bio_method = BIO_meth_new(type, "ClambHook OpenVPN control");
    if (ch_ovpn_bio_method == NULL ||
        BIO_meth_set_create(ch_ovpn_bio_method, ch_ovpn_bio_create) != 1 ||
        BIO_meth_set_destroy(ch_ovpn_bio_method, ch_ovpn_bio_destroy) != 1 ||
        BIO_meth_set_read(ch_ovpn_bio_method, ch_ovpn_bio_read) != 1 ||
        BIO_meth_set_write(ch_ovpn_bio_method, ch_ovpn_bio_write) != 1 ||
        BIO_meth_set_ctrl(ch_ovpn_bio_method, ch_ovpn_bio_control) != 1) {
        BIO_meth_free(ch_ovpn_bio_method);
        ch_ovpn_bio_method = NULL;
    }
}

typedef struct ch_ovpn_buffer {
    uint8_t *bytes;
    size_t length;
    size_t capacity;
} ch_ovpn_buffer;

static int ch_ovpn_buffer_append(ch_ovpn_buffer *buffer,
                                 const void *bytes, size_t length) {
    if (buffer->length > SIZE_MAX - length) return 0;
    size_t required = buffer->length + length;
    if (required > buffer->capacity) {
        size_t capacity = buffer->capacity == 0U ? 256U : buffer->capacity;
        while (capacity < required) {
            if (capacity > SIZE_MAX / 2U) return 0;
            capacity *= 2U;
        }
        uint8_t *next = realloc(buffer->bytes, capacity);
        if (next == NULL) return 0;
        buffer->bytes = next;
        buffer->capacity = capacity;
    }
    memcpy(buffer->bytes + buffer->length, bytes, length);
    buffer->length += length;
    return 1;
}

static int ch_ovpn_append_len_string(ch_ovpn_buffer *buffer,
                                     const char *value) {
    size_t length = value == NULL || value[0] == '\0' ? 0U :
                                                       strlen(value) + 1U;
    if (length > UINT16_MAX) return 0;
    uint8_t header[2];
    ch_ovpn_write_u16(header, (uint16_t)length);
    return ch_ovpn_buffer_append(buffer, header, sizeof(header)) &&
        (length == 0U || (ch_ovpn_buffer_append(
            buffer, value, length - 1U) &&
            ch_ovpn_buffer_append(buffer, "", 1U)));
}

static int ch_ovpn_build_key_method(ch_ovpn_session *session,
                                    uint8_t **out_bytes,
                                    size_t *out_length) {
    uint8_t pre_master[48];
    uint8_t random_one[32];
    uint8_t random_two[32];
    if (RAND_bytes(pre_master, sizeof(pre_master)) != 1 ||
        RAND_bytes(random_one, sizeof(random_one)) != 1 ||
        RAND_bytes(random_two, sizeof(random_two)) != 1) return 0;
    static const char options[] =
        "V4,dev-type tun,link-mtu 1558,tun-mtu 1500,proto UDPv4,"
        "cipher AES-256-GCM,auth SHA256,keysize 0,key-method 2,tls-client";
    const char *advertised = "AES-256-GCM:CHACHA20-POLY1305";
    char peer_info[512];
    int peer_info_length = snprintf(
        peer_info, sizeof(peer_info),
        "IV_VER=2.6.0\nIV_PLAT=linux\nIV_PROTO=30\nIV_CIPHERS=%s\n"
        "IV_NCP=2\nIV_LZO=0\nIV_LZ4=0\nIV_COMP_STUB=0\n"
        "IV_COMP_STUBv2=0\n", advertised);
    uint8_t header[5] = {0U, 0U, 0U, 0U, 2U};
    ch_ovpn_buffer buffer = {0};
    int built = peer_info_length > 0 &&
        (size_t)peer_info_length < sizeof(peer_info) &&
        ch_ovpn_buffer_append(&buffer, header, sizeof(header)) &&
        ch_ovpn_buffer_append(&buffer, pre_master, sizeof(pre_master)) &&
        ch_ovpn_buffer_append(&buffer, random_one, sizeof(random_one)) &&
        ch_ovpn_buffer_append(&buffer, random_two, sizeof(random_two)) &&
        ch_ovpn_append_len_string(&buffer, options) &&
        ch_ovpn_append_len_string(&buffer, session->config.username) &&
        ch_ovpn_append_len_string(&buffer, session->config.password) &&
        ch_ovpn_append_len_string(&buffer, peer_info);
    OPENSSL_cleanse(pre_master, sizeof(pre_master));
    OPENSSL_cleanse(random_one, sizeof(random_one));
    OPENSSL_cleanse(random_two, sizeof(random_two));
    if (!built) {
        free(buffer.bytes);
        return 0;
    }
    *out_bytes = buffer.bytes;
    *out_length = buffer.length;
    return 1;
}

static int ch_ovpn_ssl_write_all(SSL *ssl, const uint8_t *bytes,
                                 size_t length) {
    while (length > 0U) {
        int amount = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int written = SSL_write(ssl, bytes, amount);
        if (written <= 0) return 0;
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_ovpn_ssl_read_all(SSL *ssl, uint8_t *bytes, size_t length) {
    while (length > 0U) {
        int amount = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int received = SSL_read(ssl, bytes, amount);
        if (received <= 0) return 0;
        bytes += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static int ch_ovpn_read_len_string(SSL *ssl) {
    uint8_t header[2];
    if (!ch_ovpn_ssl_read_all(ssl, header, sizeof(header))) return 0;
    size_t length = ((size_t)header[0] << 8U) | header[1];
    if (length == 0U) return 1;
    uint8_t *value = malloc(length);
    if (value == NULL) return 0;
    int read = ch_ovpn_ssl_read_all(ssl, value, length);
    free(value);
    return read;
}

static char *ch_ovpn_read_push_reply(SSL *ssl, ch_error *error) {
    ch_ovpn_buffer buffer = {0};
    for (;;) {
        uint8_t byte = 0U;
        if (!ch_ovpn_ssl_read_all(ssl, &byte, 1U)) {
            free(buffer.bytes);
            ch_error_set(error, CH_ERROR_IO,
                         "openvpn: read PUSH_REPLY over TLS");
            return NULL;
        }
        if (byte == 0U) break;
        if (buffer.length >= 65535U ||
            !ch_ovpn_buffer_append(&buffer, &byte, 1U)) {
            free(buffer.bytes);
            ch_error_set(error, CH_ERROR_PARSE,
                         "openvpn: PUSH_REPLY is too large");
            return NULL;
        }
    }
    if (!ch_ovpn_buffer_append(&buffer, "", 1U)) {
        free(buffer.bytes);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "openvpn: terminate PUSH_REPLY");
        return NULL;
    }
    char *message = (char *)buffer.bytes;
    if (strncmp(message, "AUTH_FAILED", 11U) == 0) {
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: server rejected authentication: %.120s",
                     message);
        free(message);
        return NULL;
    }
    static const char prefix[] = "PUSH_REPLY,";
    if (strncmp(message, prefix, sizeof(prefix) - 1U) != 0) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "openvpn: expected PUSH_REPLY, got %.120s", message);
        free(message);
        return NULL;
    }
    memmove(message, message + sizeof(prefix) - 1U,
            strlen(message + sizeof(prefix) - 1U) + 1U);
    return message;
}

typedef struct ch_ovpn_push {
    ch_ovpn_cipher cipher;
    uint32_t peer_id;
    unsigned int mtu;
    char **addresses;
    size_t address_count;
    char **dns_servers;
    size_t dns_server_count;
} ch_ovpn_push;

static void ch_ovpn_strings_free(char **values, size_t count) {
    for (size_t index = 0U; index < count; ++index) free(values[index]);
    free(values);
}

static void ch_ovpn_push_free(ch_ovpn_push *push) {
    ch_ovpn_strings_free(push->addresses, push->address_count);
    ch_ovpn_strings_free(push->dns_servers, push->dns_server_count);
    memset(push, 0, sizeof(*push));
}

static int ch_ovpn_push_add(char ***values, size_t *count,
                            const char *value) {
    char **next = realloc(*values, (*count + 1U) * sizeof(**values));
    if (next == NULL) return 0;
    *values = next;
    next[*count] = ch_strdup(value);
    if (next[*count] == NULL) return 0;
    ++*count;
    return 1;
}

static char *ch_ovpn_trim(char *value) {
    while (isspace((unsigned char)*value)) ++value;
    char *end = value + strlen(value);
    while (end > value && isspace((unsigned char)end[-1])) --end;
    *end = '\0';
    return value;
}

static int ch_ovpn_parse_push(char *body, ch_ovpn_push *push,
                              ch_error *error) {
    memset(push, 0, sizeof(*push));
    char *save = NULL;
    for (char *entry = strtok_r(body, ",", &save); entry != NULL;
         entry = strtok_r(NULL, ",", &save)) {
        entry = ch_ovpn_trim(entry);
        char *field_save = NULL;
        char *name = strtok_r(entry, " \t", &field_save);
        char *first = strtok_r(NULL, " \t", &field_save);
        char *second = strtok_r(NULL, " \t", &field_save);
        if (name == NULL) continue;
        if (strcmp(name, "ifconfig") == 0) {
            uint8_t parsed[16];
            if (first == NULL || (inet_pton(AF_INET, first, parsed) != 1 &&
                                  inet_pton(AF_INET6, first, parsed) != 1) ||
                !ch_ovpn_push_add(&push->addresses,
                                  &push->address_count, first)) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "openvpn: PUSH_REPLY ifconfig is invalid");
                return 0;
            }
        } else if (strcmp(name, "peer-id") == 0) {
            char *end = NULL;
            errno = 0;
            unsigned long value = first == NULL ? ULONG_MAX :
                strtoul(first, &end, 10);
            if (errno != 0 || first == NULL || end == first || *end != '\0' ||
                value > UINT32_MAX) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "openvpn: PUSH_REPLY peer-id is invalid");
                return 0;
            }
            push->peer_id = (uint32_t)value;
        } else if (strcmp(name, "cipher") == 0 && first != NULL) {
            push->cipher = ch_ovpn_cipher_from_name(first);
            if (push->cipher == 0) {
                ch_error_set(error, CH_ERROR_UNSUPPORTED,
                             "openvpn: server selected unsupported cipher %s",
                             first);
                return 0;
            }
        } else if (strcmp(name, "tun-mtu") == 0 && first != NULL) {
            char *end = NULL;
            unsigned long value = strtoul(first, &end, 10);
            if (end != first && *end == '\0' && value <= UINT16_MAX) {
                push->mtu = (unsigned int)value;
            }
        } else if (strcmp(name, "dhcp-option") == 0 && first != NULL &&
                   second != NULL && strcasecmp(first, "DNS") == 0) {
            uint8_t parsed[16];
            if ((inet_pton(AF_INET, second, parsed) == 1 ||
                 inet_pton(AF_INET6, second, parsed) == 1) &&
                !ch_ovpn_push_add(&push->dns_servers,
                                  &push->dns_server_count, second)) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "openvpn: copy pushed DNS server");
                return 0;
            }
        } else if (strcmp(name, "compress") == 0 ||
                   strcmp(name, "comp-lzo") == 0 ||
                   strcmp(name, "comp-flags") == 0) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "openvpn: compression is not supported");
            return 0;
        }
    }
    if (push->address_count == 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "openvpn: PUSH_REPLY did not set ifconfig");
        return 0;
    }
    return 1;
}

static int ch_ovpn_data_seal(ch_ovpn_session *session,
                             const uint8_t *plain, size_t plain_length,
                             uint8_t **out_packet, size_t *out_length) {
    ch_ovpn_data *data = &session->data;
    if (!session->data_ready || plain == NULL || plain_length == 0U ||
        data->next_packet_id == 0U ||
        data->next_packet_id >= CH_OVPN_DATA_REKEY_THRESHOLD ||
        plain_length > SIZE_MAX - 24U) return 0;
    uint32_t packet_id = data->next_packet_id++;
    size_t length = 8U + 16U + plain_length;
    uint8_t *packet = malloc(length);
    if (packet == NULL) return 0;
    packet[0] = (uint8_t)((CH_OVPN_OPCODE_DATA_V2 << 3U) |
                          (data->key_id & 7U));
    packet[1] = (uint8_t)(data->peer_id >> 16U);
    packet[2] = (uint8_t)(data->peer_id >> 8U);
    packet[3] = (uint8_t)data->peer_id;
    ch_ovpn_write_u32(packet + 4U, packet_id);
    uint8_t nonce[12];
    memcpy(nonce, packet + 4U, 4U);
    memcpy(nonce + 4U, data->send_iv, sizeof(data->send_iv));
    int result = data->cipher == CH_OVPN_CIPHER_AES_256_GCM ?
        cnet_aes256gcm_encrypt(
            data->send_key, nonce, plain, plain_length, packet, 8U,
            packet + 24U, packet + 8U) :
        cnet_chacha20poly1305_encrypt(
            data->send_key, nonce, plain, plain_length, packet, 8U,
            packet + 24U, packet + 8U);
    OPENSSL_cleanse(nonce, sizeof(nonce));
    if (result != CNET_OK) {
        free(packet);
        return 0;
    }
    *out_packet = packet;
    *out_length = length;
    return 1;
}

static int ch_ovpn_replay_available(const ch_ovpn_data *data,
                                    uint32_t packet_id) {
    if (packet_id == 0U) return 0;
    if (packet_id > data->highest_packet_id) return 1;
    uint32_t difference = data->highest_packet_id - packet_id;
    return difference < 64U &&
        (data->replay_window & (UINT64_C(1) << difference)) == 0U;
}

static void ch_ovpn_replay_commit(ch_ovpn_data *data,
                                  uint32_t packet_id) {
    if (packet_id > data->highest_packet_id) {
        uint32_t shift = packet_id - data->highest_packet_id;
        data->replay_window = shift >= 64U ? UINT64_C(1) :
            (data->replay_window << shift) | UINT64_C(1);
        data->highest_packet_id = packet_id;
    } else {
        uint32_t difference = data->highest_packet_id - packet_id;
        if (difference < 64U) {
            data->replay_window |= UINT64_C(1) << difference;
        }
    }
}

static uint8_t *ch_ovpn_data_open(ch_ovpn_session *session,
                                  const uint8_t *packet, size_t length,
                                  size_t *out_length) {
    *out_length = 0U;
    if (!session->data_ready || packet == NULL || length <= 24U ||
        packet[0] >> 3U != CH_OVPN_OPCODE_DATA_V2 ||
        (packet[0] & 7U) != session->data.key_id) return NULL;
    uint32_t packet_id = ch_ovpn_read_u32(packet + 4U);
    if (!ch_ovpn_replay_available(&session->data, packet_id)) return NULL;
    size_t cipher_length = length - 24U;
    uint8_t *plain = malloc(cipher_length);
    if (plain == NULL) return NULL;
    uint8_t nonce[12];
    memcpy(nonce, packet + 4U, 4U);
    memcpy(nonce + 4U, session->data.receive_iv,
           sizeof(session->data.receive_iv));
    int result = session->data.cipher == CH_OVPN_CIPHER_AES_256_GCM ?
        cnet_aes256gcm_decrypt(
            session->data.receive_key, nonce, packet + 24U, cipher_length,
            packet, 8U, packet + 8U, plain) :
        cnet_chacha20poly1305_decrypt(
            session->data.receive_key, nonce, packet + 24U, cipher_length,
            packet, 8U, packet + 8U, plain);
    OPENSSL_cleanse(nonce, sizeof(nonce));
    if (result != CNET_OK) {
        free(plain);
        return NULL;
    }
    ch_ovpn_replay_commit(&session->data, packet_id);
    *out_length = cipher_length;
    return plain;
}

static void ch_ovpn_install_key_block(ch_ovpn_session *session,
                                      ch_ovpn_cipher cipher,
                                      uint32_t peer_id,
                                      const uint8_t *key_block) {
    memset(&session->data, 0, sizeof(session->data));
    session->data.cipher = cipher;
    session->data.key_id = 0U;
    session->data.peer_id = peer_id;
    memcpy(session->data.send_key, key_block, 32U);
    memcpy(session->data.send_iv, key_block + CH_OVPN_SLOT_SIZE, 8U);
    memcpy(session->data.receive_key,
           key_block + 2U * CH_OVPN_SLOT_SIZE, 32U);
    memcpy(session->data.receive_iv,
           key_block + 3U * CH_OVPN_SLOT_SIZE, 8U);
    session->data.next_packet_id = 1U;
    session->data_ready = 1;
}

static void ch_ovpn_tunnel_write(const uint8_t *packet, size_t length,
                                 void *context) {
    ch_ovpn_session *session = context;
    if (session == NULL) return;
    (void)pthread_mutex_lock(&session->mutex);
    if (!session->stopping && !session->failed && session->data_ready) {
        uint8_t *outer = NULL;
        size_t outer_length = 0U;
        if (ch_ovpn_data_seal(session, packet, length,
                              &outer, &outer_length)) {
            (void)ch_ovpn_send_raw(session, outer, outer_length);
        } else if (session->data.next_packet_id >=
                   CH_OVPN_DATA_REKEY_THRESHOLD) {
            session->failed = 1;
        }
        free(outer);
    }
    (void)pthread_mutex_unlock(&session->mutex);
}

static char *ch_ovpn_remote_host(const char *remote) {
    const char *start = remote;
    const char *end = NULL;
    if (remote[0] == '[') {
        start = remote + 1U;
        end = strchr(start, ']');
    } else {
        end = strrchr(remote, ':');
    }
    if (end == NULL || end <= start) return NULL;
    size_t length = (size_t)(end - start);
    char *host = malloc(length + 1U);
    if (host == NULL) return NULL;
    memcpy(host, start, length);
    host[length] = '\0';
    return host;
}

static int ch_ovpn_handshake(ch_ovpn_session *session, ch_error *error) {
    session->handshake_deadline = ch_ovpn_now_milliseconds() +
        CH_OVPN_HANDSHAKE_MILLISECONDS;
    if (!ch_ovpn_send_control(
            session, CH_OVPN_OPCODE_HARD_RESET_CLIENT_V2, NULL, 0U)) {
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: send HARD_RESET_CLIENT_V2");
        return 0;
    }
    uint64_t reset_deadline = ch_ovpn_now_milliseconds() +
        CH_OVPN_RESET_MILLISECONDS;
    if (reset_deadline > session->handshake_deadline) {
        reset_deadline = session->handshake_deadline;
    }
    if (!ch_ovpn_pump(session, reset_deadline, 1, 0)) {
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: receive HARD_RESET_SERVER_V2: %s",
                     strerror(errno));
        return 0;
    }

    (void)pthread_once(&ch_ovpn_bio_once, ch_ovpn_initialize_bio);
    SSL *ssl = ch_ovpn_bio_method == NULL ? NULL :
        SSL_new(session->tls_context);
    BIO *bio = ch_ovpn_bio_method == NULL ? NULL :
        BIO_new(ch_ovpn_bio_method);
    if (ssl == NULL || bio == NULL) {
        SSL_free(ssl);
        BIO_free(bio);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "openvpn: initialize TLS control channel");
        return 0;
    }
    BIO_set_data(bio, session);
    SSL_set_bio(ssl, bio, bio);
    char *host = ch_ovpn_nonempty(session->config.server_cn) ?
        ch_strdup(session->config.server_cn) :
        ch_ovpn_remote_host(session->config.remote);
    if (host != NULL && host[0] != '\0') {
        (void)SSL_set_tlsext_host_name(ssl, host);
        if (!session->config.skip_verify && SSL_set1_host(ssl, host) != 1) {
            free(host);
            SSL_free(ssl);
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "openvpn: configure certificate hostname");
            return 0;
        }
    }
    free(host);
    if (SSL_connect(ssl) != 1) {
        unsigned long code = ERR_get_error();
        char detail[160];
        ERR_error_string_n(code, detail, sizeof(detail));
        SSL_free(ssl);
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: TLS handshake: %s", detail);
        return 0;
    }
    uint8_t *key_method = NULL;
    size_t key_method_length = 0U;
    if (!ch_ovpn_build_key_method(session, &key_method,
                                  &key_method_length) ||
        !ch_ovpn_ssl_write_all(ssl, key_method, key_method_length)) {
        free(key_method);
        SSL_free(ssl);
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: write key-method 2");
        return 0;
    }
    OPENSSL_cleanse(key_method, key_method_length);
    free(key_method);
    uint8_t server_header[69];
    if (!ch_ovpn_ssl_read_all(ssl, server_header, sizeof(server_header)) ||
        server_header[4] != 2U || !ch_ovpn_read_len_string(ssl) ||
        !ch_ovpn_read_len_string(ssl) || !ch_ovpn_read_len_string(ssl) ||
        !ch_ovpn_read_len_string(ssl)) {
        SSL_free(ssl);
        ch_error_set(error, CH_ERROR_PARSE,
                     "openvpn: invalid server key-method 2 reply");
        return 0;
    }
    static const uint8_t push_request[] = "PUSH_REQUEST\0";
    if (!ch_ovpn_ssl_write_all(ssl, push_request,
                               sizeof(push_request) - 1U)) {
        SSL_free(ssl);
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: send PUSH_REQUEST");
        return 0;
    }
    char *push_reply = ch_ovpn_read_push_reply(ssl, error);
    if (push_reply == NULL) {
        SSL_free(ssl);
        return 0;
    }
    ch_ovpn_push push;
    if (!ch_ovpn_parse_push(push_reply, &push, error)) {
        free(push_reply);
        ch_ovpn_push_free(&push);
        SSL_free(ssl);
        return 0;
    }
    free(push_reply);
    ch_ovpn_cipher cipher = push.cipher != 0 ? push.cipher :
        (session->config.pinned_cipher != 0 ?
            session->config.pinned_cipher : CH_OVPN_CIPHER_AES_256_GCM);
    uint8_t key_block[CH_OVPN_KEY_BLOCK_SIZE];
    static const char exporter[] = "EXPORTER-OpenVPN-datakeys";
    if (SSL_export_keying_material(
            ssl, key_block, sizeof(key_block), exporter,
            sizeof(exporter) - 1U, NULL, 0U, 0) != 1) {
        ch_ovpn_push_free(&push);
        SSL_free(ssl);
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "openvpn: TLS EKM export failed (server must enable "
                     "tls-ekm)");
        return 0;
    }
    ch_ovpn_install_key_block(session, cipher, push.peer_id, key_block);
    OPENSSL_cleanse(key_block, sizeof(key_block));
    session->addresses = push.addresses;
    session->address_count = push.address_count;
    session->dns_servers = push.dns_servers;
    session->dns_server_count = push.dns_server_count;
    push.addresses = NULL;
    push.address_count = 0U;
    push.dns_servers = NULL;
    push.dns_server_count = 0U;
    session->mtu = push.mtu > 0U ? push.mtu : session->config.tun_mtu;
    (void)SSL_shutdown(ssl);
    SSL_free(ssl);
    ch_ovpn_push_free(&push);
    return 1;
}

static void ch_ovpn_drain_wake(ch_ovpn_session *session) {
    uint8_t bytes[64];
    while (read(session->wake_read, bytes, sizeof(bytes)) > 0) {
    }
}

static void *ch_ovpn_worker_main(void *opaque) {
    ch_ovpn_session *session = opaque;
    uint8_t packet[65536U];
    for (;;) {
        struct pollfd descriptors[2] = {
            {.fd = session->wake_read, .events = POLLIN},
            {.fd = session->socket, .events = POLLIN}
        };
        int ready;
        do {
            ready = poll(descriptors, 2U, CH_OVPN_TICK_MILLISECONDS);
        } while (ready < 0 && errno == EINTR);
        if ((descriptors[0].revents & POLLIN) != 0) {
            ch_ovpn_drain_wake(session);
        }
        if (ready > 0 && (descriptors[1].revents & POLLIN) != 0) {
            for (;;) {
                ssize_t length = recv(session->socket, packet,
                                      sizeof(packet), 0);
                if (length < 0 && errno == EINTR) continue;
                if (length < 0 && (errno == EAGAIN ||
                                   errno == EWOULDBLOCK)) break;
                if (length <= 0) {
                    (void)pthread_mutex_lock(&session->mutex);
                    session->failed = 1;
                    (void)pthread_mutex_unlock(&session->mutex);
                    break;
                }
                uint8_t *inner = NULL;
                size_t inner_length = 0U;
                (void)pthread_mutex_lock(&session->mutex);
                if (packet[0] >> 3U == CH_OVPN_OPCODE_DATA_V2) {
                    inner = ch_ovpn_data_open(
                        session, packet, (size_t)length, &inner_length);
                } else {
                    (void)ch_ovpn_handle_control(
                        session, packet, (size_t)length);
                }
                (void)pthread_mutex_unlock(&session->mutex);
                if (inner != NULL) {
                    ch_error ignored;
                    (void)ch_tunnel_stack_inject(
                        session->tunnel, inner, inner_length, &ignored);
                }
                free(inner);
            }
        }
        (void)pthread_mutex_lock(&session->mutex);
        if (!session->stopping && !ch_ovpn_retransmit(session)) {
            session->failed = 1;
        }
        int stopping = session->stopping;
        (void)pthread_mutex_unlock(&session->mutex);
        if (stopping) break;
    }
    OPENSSL_cleanse(packet, sizeof(packet));
    return NULL;
}

static void ch_ovpn_session_destroy(ch_ovpn_session *session) {
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
    ch_ovpn_close_descriptor(&session->socket);
    ch_ovpn_close_descriptor(&session->wake_read);
    ch_ovpn_close_descriptor(&session->wake_write);
    ch_ovpn_outstanding *pending = session->outstanding;
    while (pending != NULL) {
        ch_ovpn_outstanding *next = pending->next;
        free(pending->bytes);
        free(pending);
        pending = next;
    }
    ch_ovpn_reorder *reorder = session->reorder;
    while (reorder != NULL) {
        ch_ovpn_reorder *next = reorder->next;
        free(reorder->payload);
        free(reorder);
        reorder = next;
    }
    free(session->control_stream);
    ch_ovpn_strings_free(session->addresses, session->address_count);
    ch_ovpn_strings_free(session->dns_servers, session->dns_server_count);
    SSL_CTX_free(session->tls_context);
    ch_ovpn_config_free(&session->config);
    OPENSSL_cleanse(&session->data, sizeof(session->data));
    OPENSSL_cleanse(session->local_session,
                    sizeof(session->local_session));
    OPENSSL_cleanse(session->remote_session,
                    sizeof(session->remote_session));
    (void)pthread_mutex_destroy(&session->mutex);
    free(session->key);
    free(session);
}

static ch_ovpn_session *ch_ovpn_session_create(
    const ch_config_table *server, char *key, ch_error *error) {
    ch_ovpn_session *session = calloc(1U, sizeof(*session));
    if (session == NULL) {
        free(key);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "openvpn: allocate session");
        return NULL;
    }
    session->key = key;
    session->socket = -1;
    session->wake_read = -1;
    session->wake_write = -1;
    if (pthread_mutex_init(&session->mutex, NULL) != 0) {
        free(session->key);
        free(session);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "openvpn: initialize session lock");
        return NULL;
    }
    if (!ch_ovpn_parse_config(server, &session->config, error)) {
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    session->tls_context = ch_ovpn_tls_context(&session->config, error);
    if (session->tls_context == NULL) {
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    session->socket = ch_ovpn_connect_udp(session->config.remote, error);
    if (session->socket < 0 || RAND_bytes(
            session->local_session, sizeof(session->local_session)) != 1) {
        if (session->socket >= 0) {
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "openvpn: generate control session ID");
        }
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    if (!ch_ovpn_handshake(session, error)) {
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    SSL_CTX_free(session->tls_context);
    session->tls_context = NULL;
    free(session->control_stream);
    session->control_stream = NULL;
    session->control_stream_offset = 0U;
    session->control_stream_length = 0U;
    if (session->mtu < 1280U || session->mtu > UINT16_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: negotiated tun_mtu is outside [1280,65535]");
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    int wake[2] = {-1, -1};
    if (pipe(wake) != 0 || !ch_ovpn_set_nonblocking(wake[0]) ||
        !ch_ovpn_set_nonblocking(wake[1])) {
        ch_ovpn_close_descriptor(&wake[0]);
        ch_ovpn_close_descriptor(&wake[1]);
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: initialize worker wake pipe");
        ch_ovpn_session_destroy(session);
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
        .packet_writer = ch_ovpn_tunnel_write,
        .packet_writer_context = session
    };
    session->tunnel = ch_tunnel_stack_create(&options, error);
    if (session->tunnel == NULL) {
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    if (pthread_create(&session->worker, NULL, ch_ovpn_worker_main,
                       session) != 0) {
        ch_error_set(error, CH_ERROR_IO,
                     "openvpn: start data-channel worker");
        ch_ovpn_session_destroy(session);
        return NULL;
    }
    session->worker_started = 1;
    return session;
}

static ch_ovpn_session *ch_ovpn_session_for_server(
    const ch_config_table *server, ch_error *error) {
    char *key = NULL;
    if (ch_config_table_json(server, &key, error) != CH_OK) return NULL;
    (void)pthread_mutex_lock(&ch_ovpn_registry_mutex);
    for (ch_ovpn_session *session = ch_ovpn_registry; session != NULL;
         session = session->next) {
        if (strcmp(session->key, key) == 0 && !session->failed) {
            free(key);
            (void)pthread_mutex_unlock(&ch_ovpn_registry_mutex);
            return session;
        }
    }
    ch_ovpn_session *session = ch_ovpn_session_create(server, key, error);
    if (session != NULL) {
        session->next = ch_ovpn_registry;
        ch_ovpn_registry = session;
    }
    (void)pthread_mutex_unlock(&ch_ovpn_registry_mutex);
    return session;
}

static int ch_ovpn_fixture_failure(ch_error *error, const char *message) {
    ch_error_set(error, CH_ERROR_INTERNAL, "openvpn fixture: %s", message);
    return 0;
}

static int ch_ovpn_fixture_data_cipher(ch_ovpn_cipher cipher,
                                       ch_error *error) {
    ch_ovpn_session sender;
    ch_ovpn_session receiver;
    memset(&sender, 0, sizeof(sender));
    memset(&receiver, 0, sizeof(receiver));
    uint8_t key_block[CH_OVPN_KEY_BLOCK_SIZE];
    for (size_t index = 0U; index < sizeof(key_block); ++index) {
        key_block[index] = (uint8_t)index;
    }
    ch_ovpn_install_key_block(&sender, cipher, UINT32_C(0x010203),
                              key_block);
    ch_ovpn_install_key_block(&receiver, cipher, UINT32_C(0x010203),
                              key_block);
    memcpy(receiver.data.receive_key, sender.data.send_key,
           sizeof(receiver.data.receive_key));
    memcpy(receiver.data.receive_iv, sender.data.send_iv,
           sizeof(receiver.data.receive_iv));
    if (memcmp(sender.data.send_key, key_block, 32U) != 0 ||
        memcmp(sender.data.send_iv, key_block + 64U, 8U) != 0 ||
        memcmp(sender.data.receive_key, key_block + 128U, 32U) != 0 ||
        memcmp(sender.data.receive_iv, key_block + 192U, 8U) != 0) {
        return ch_ovpn_fixture_failure(error, "TLS-EKM key split");
    }

    static const uint8_t plain[] = "authenticated OpenVPN data";
    uint8_t *packet = NULL;
    size_t packet_length = 0U;
    if (!ch_ovpn_data_seal(&sender, plain, sizeof(plain),
                           &packet, &packet_length) ||
        packet_length != sizeof(plain) + 24U ||
        packet[0] != (uint8_t)(CH_OVPN_OPCODE_DATA_V2 << 3U) ||
        packet[1] != 1U || packet[2] != 2U || packet[3] != 3U ||
        ch_ovpn_read_u32(packet + 4U) != 1U) {
        free(packet);
        return ch_ovpn_fixture_failure(error, "AEAD wire layout");
    }
    uint8_t *tampered = malloc(packet_length);
    if (tampered == NULL) {
        free(packet);
        return ch_ovpn_fixture_failure(error, "allocate tamper packet");
    }
    memcpy(tampered, packet, packet_length);
    tampered[8] ^= 1U;
    size_t opened_length = 0U;
    uint8_t *opened = ch_ovpn_data_open(
        &receiver, tampered, packet_length, &opened_length);
    free(tampered);
    if (opened != NULL) {
        free(opened);
        free(packet);
        return ch_ovpn_fixture_failure(error, "accepted tampered tag");
    }
    opened = ch_ovpn_data_open(
        &receiver, packet, packet_length, &opened_length);
    if (opened == NULL || opened_length != sizeof(plain) ||
        memcmp(opened, plain, sizeof(plain)) != 0) {
        free(opened);
        free(packet);
        return ch_ovpn_fixture_failure(
            error, "tamper advanced replay window");
    }
    free(opened);
    opened = ch_ovpn_data_open(
        &receiver, packet, packet_length, &opened_length);
    free(packet);
    if (opened != NULL) {
        free(opened);
        return ch_ovpn_fixture_failure(error, "accepted replay");
    }

    memset(&receiver.data.highest_packet_id, 0,
           sizeof(receiver.data.highest_packet_id));
    receiver.data.replay_window = 0U;
    sender.data.next_packet_id = 1U;
    uint8_t *first = NULL;
    size_t first_length = 0U;
    for (unsigned int index = 0U; index < 80U; ++index) {
        uint8_t value = (uint8_t)index;
        packet = NULL;
        packet_length = 0U;
        if (!ch_ovpn_data_seal(&sender, &value, 1U,
                               &packet, &packet_length)) {
            free(first);
            return ch_ovpn_fixture_failure(error, "seal replay window");
        }
        if (index == 0U) {
            first = packet;
            first_length = packet_length;
            packet = NULL;
        } else if (index >= 20U) {
            opened = ch_ovpn_data_open(
                &receiver, packet, packet_length, &opened_length);
            if (opened == NULL || opened_length != 1U ||
                opened[0] != value) {
                free(opened);
                free(packet);
                free(first);
                return ch_ovpn_fixture_failure(error, "replay window flow");
            }
            free(opened);
        }
        free(packet);
    }
    opened = ch_ovpn_data_open(
        &receiver, first, first_length, &opened_length);
    free(first);
    if (opened != NULL) {
        free(opened);
        return ch_ovpn_fixture_failure(error, "accepted old packet");
    }

    sender.data.next_packet_id = CH_OVPN_DATA_REKEY_THRESHOLD - 1U;
    packet = NULL;
    packet_length = 0U;
    if (!ch_ovpn_data_seal(&sender, plain, sizeof(plain),
                           &packet, &packet_length)) {
        return ch_ovpn_fixture_failure(error, "last pre-rekey packet");
    }
    free(packet);
    packet = NULL;
    if (ch_ovpn_data_seal(&sender, plain, sizeof(plain),
                          &packet, &packet_length)) {
        free(packet);
        return ch_ovpn_fixture_failure(error, "missed rekey threshold");
    }
    OPENSSL_cleanse(&sender, sizeof(sender));
    OPENSSL_cleanse(&receiver, sizeof(receiver));
    OPENSSL_cleanse(key_block, sizeof(key_block));
    return 1;
}

static int ch_ovpn_fixture_key_method_and_push(ch_error *error) {
    ch_ovpn_session session;
    memset(&session, 0, sizeof(session));
    session.config.username = (char *)"alice";
    session.config.password = (char *)"hunter2";
    uint8_t *message = NULL;
    size_t length = 0U;
    if (!ch_ovpn_build_key_method(&session, &message, &length) ||
        length < 117U || message[0] != 0U || message[1] != 0U ||
        message[2] != 0U || message[3] != 0U || message[4] != 2U) {
        free(message);
        return ch_ovpn_fixture_failure(error, "key-method 2 header");
    }
    size_t offset = 117U;
    const char *expected[4] = {NULL, "alice", "hunter2", NULL};
    for (size_t field = 0U; field < 4U; ++field) {
        if (offset + 2U > length) {
            free(message);
            return ch_ovpn_fixture_failure(error, "key-method length");
        }
        size_t field_length = ((size_t)message[offset] << 8U) |
                              message[offset + 1U];
        offset += 2U;
        if (field_length > length - offset ||
            (field_length > 0U &&
             message[offset + field_length - 1U] != 0U)) {
            free(message);
            return ch_ovpn_fixture_failure(error, "key-method string");
        }
        if (expected[field] != NULL &&
            (field_length != strlen(expected[field]) + 1U ||
             memcmp(message + offset, expected[field],
                    field_length - 1U) != 0)) {
            free(message);
            return ch_ovpn_fixture_failure(error, "key-method credentials");
        }
        if (field == 0U && (field_length == 0U || strstr(
                (const char *)message + offset,
                "cipher AES-256-GCM") == NULL)) {
            free(message);
            return ch_ovpn_fixture_failure(error, "key-method options");
        }
        if (field == 3U && (field_length == 0U || strstr(
                (const char *)message + offset,
                "IV_CIPHERS=AES-256-GCM:CHACHA20-POLY1305") == NULL)) {
            free(message);
            return ch_ovpn_fixture_failure(error, "key-method peer info");
        }
        offset += field_length;
    }
    OPENSSL_cleanse(message, length);
    free(message);
    if (offset != length) {
        return ch_ovpn_fixture_failure(error, "key-method trailing data");
    }

    char body[] =
        "route-gateway 10.8.0.1,ifconfig 10.8.0.2 255.255.255.0,"
        "cipher AES-256-GCM,peer-id 42,dhcp-option DNS 8.8.8.8,"
        "dhcp-option DNS 2001:4860:4860::8888,tun-mtu 1500,ping 10";
    ch_ovpn_push push;
    if (!ch_ovpn_parse_push(body, &push, error) ||
        push.cipher != CH_OVPN_CIPHER_AES_256_GCM ||
        push.peer_id != 42U || push.mtu != 1500U ||
        push.address_count != 1U ||
        strcmp(push.addresses[0], "10.8.0.2") != 0 ||
        push.dns_server_count != 2U) {
        ch_ovpn_push_free(&push);
        return ch_ovpn_fixture_failure(error, "PUSH_REPLY fields");
    }
    ch_ovpn_push_free(&push);
    char compressed[] = "ifconfig 10.8.0.2 255.255.255.0,compress lz4";
    if (ch_ovpn_parse_push(compressed, &push, error)) {
        ch_ovpn_push_free(&push);
        return ch_ovpn_fixture_failure(error, "accepted compression");
    }
    ch_ovpn_push_free(&push);
    char missing_address[] = "cipher AES-256-GCM,peer-id 7";
    if (ch_ovpn_parse_push(missing_address, &push, error)) {
        ch_ovpn_push_free(&push);
        return ch_ovpn_fixture_failure(error, "accepted missing ifconfig");
    }
    ch_ovpn_push_free(&push);
    ch_error_clear(error);
    return 1;
}

static void ch_ovpn_fixture_session_clear(ch_ovpn_session *session) {
    ch_ovpn_outstanding *pending = session->outstanding;
    while (pending != NULL) {
        ch_ovpn_outstanding *next = pending->next;
        free(pending->bytes);
        free(pending);
        pending = next;
    }
    ch_ovpn_reorder *reorder = session->reorder;
    while (reorder != NULL) {
        ch_ovpn_reorder *next = reorder->next;
        free(reorder->payload);
        free(reorder);
        reorder = next;
    }
    free(session->control_stream);
}

static int ch_ovpn_fixture_reliable(ch_error *error) {
    int descriptors[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_DGRAM, 0, descriptors) != 0) {
        return ch_ovpn_fixture_failure(error, "control socketpair");
    }
    ch_ovpn_session local;
    ch_ovpn_session peer;
    memset(&local, 0, sizeof(local));
    memset(&peer, 0, sizeof(peer));
    local.socket = descriptors[0];
    peer.socket = descriptors[1];
    for (size_t index = 0U; index < CH_OVPN_SESSION_ID_LENGTH; ++index) {
        local.local_session[index] = (uint8_t)(0x10U + index);
        peer.local_session[index] = (uint8_t)(0x20U + index);
    }
    uint8_t *packets[3] = {NULL, NULL, NULL};
    size_t lengths[3] = {0U, 0U, 0U};
    static const char *payloads[3] = {"first", "second", "third"};
    for (size_t index = 0U; index < 3U; ++index) {
        if (!ch_ovpn_control_encode(
                &peer, CH_OVPN_OPCODE_CONTROL_V1,
                (const uint8_t *)payloads[index], strlen(payloads[index]),
                0, &packets[index], &lengths[index])) {
            goto failed;
        }
    }
    if (!ch_ovpn_handle_control(&local, packets[1], lengths[1]) ||
        !ch_ovpn_handle_control(&local, packets[2], lengths[2]) ||
        local.control_stream_length != 0U ||
        !ch_ovpn_handle_control(&local, packets[0], lengths[0])) {
        goto failed;
    }
    static const char ordered[] = "firstsecondthird";
    if (local.control_stream_length != sizeof(ordered) - 1U ||
        memcmp(local.control_stream, ordered, sizeof(ordered) - 1U) != 0 ||
        local.next_expected_id != 3U) goto failed;
    size_t delivered = local.control_stream_length;
    if (!ch_ovpn_handle_control(&local, packets[0], lengths[0]) ||
        local.control_stream_length != delivered) goto failed;
    uint8_t original = packets[0][1];
    packets[0][1] ^= 1U;
    if (ch_ovpn_handle_control(&local, packets[0], lengths[0])) goto failed;
    packets[0][1] = original;
    packets[0][0] |= 1U;
    if (ch_ovpn_handle_control(&local, packets[0], lengths[0])) goto failed;
    packets[0][0] &= (uint8_t)~1U;
    if (ch_ovpn_handle_control(&local, packets[0], 9U)) goto failed;

    uint8_t *outbound = NULL;
    size_t outbound_length = 0U;
    if (!ch_ovpn_control_encode(
            &local, CH_OVPN_OPCODE_CONTROL_V1,
            (const uint8_t *)"pending", 7U, 1,
            &outbound, &outbound_length) || local.outstanding == NULL) {
        free(outbound);
        goto failed;
    }
    free(outbound);
    peer.have_remote_session = 1;
    memcpy(peer.remote_session, local.local_session,
           CH_OVPN_SESSION_ID_LENGTH);
    peer.pending_acks[0] = local.outstanding->id;
    peer.pending_ack_count = 1U;
    uint8_t *ack = NULL;
    size_t ack_length = 0U;
    if (!ch_ovpn_control_encode(&peer, CH_OVPN_OPCODE_ACK_V1, NULL, 0U,
                                0, &ack, &ack_length)) goto failed;
    size_t ack_remote = 1U + CH_OVPN_SESSION_ID_LENGTH + 1U + 4U;
    ack[ack_remote] ^= 1U;
    if (ch_ovpn_handle_control(&local, ack, ack_length) ||
        local.outstanding == NULL) {
        free(ack);
        goto failed;
    }
    ack[ack_remote] ^= 1U;
    if (!ch_ovpn_handle_control(&local, ack, ack_length) ||
        local.outstanding != NULL) {
        free(ack);
        goto failed;
    }
    free(ack);

    local.outstanding = calloc(1U, sizeof(*local.outstanding));
    if (local.outstanding == NULL) goto failed;
    local.outstanding->bytes = malloc(1U);
    if (local.outstanding->bytes == NULL) goto failed;
    local.outstanding->bytes[0] = 0U;
    local.outstanding->length = 1U;
    local.outstanding->retries = 10U;
    local.outstanding->sent_at = 0U;
    if (ch_ovpn_retransmit(&local)) goto failed;

    for (size_t index = 0U; index < 3U; ++index) free(packets[index]);
    ch_ovpn_fixture_session_clear(&local);
    ch_ovpn_fixture_session_clear(&peer);
    (void)close(descriptors[0]);
    (void)close(descriptors[1]);
    return 1;

failed:
    for (size_t index = 0U; index < 3U; ++index) free(packets[index]);
    ch_ovpn_fixture_session_clear(&local);
    ch_ovpn_fixture_session_clear(&peer);
    (void)close(descriptors[0]);
    (void)close(descriptors[1]);
    return ch_ovpn_fixture_failure(error, "reliable control channel");
}

ch_status ch_protocol_openvpn_test_fixtures(ch_error *error) {
    ch_error_clear(error);
    if (!ch_ovpn_fixture_data_cipher(CH_OVPN_CIPHER_AES_256_GCM, error) ||
        !ch_ovpn_fixture_data_cipher(
            CH_OVPN_CIPHER_CHACHA20_POLY1305, error) ||
        !ch_ovpn_fixture_key_method_and_push(error) ||
        !ch_ovpn_fixture_reliable(error)) {
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    return CH_OK;
}

ch_status ch_protocol_openvpn_dial(const ch_config_table *server,
                                   const char *target,
                                   int *out_descriptor,
                                   ch_error *error) {
    ch_error_clear(error);
    if (server == NULL || target == NULL || out_descriptor == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: server, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    ch_ovpn_session *session = ch_ovpn_session_for_server(server, error);
    if (session == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                               error->code;
    return ch_tunnel_stack_dial_tcp(session->tunnel, target,
                                    out_descriptor, error);
}

ch_status ch_protocol_openvpn_open_packet(
    const ch_config_table *server,
    ch_tunnel_packet **out_packet,
    ch_error *error) {
    ch_error_clear(error);
    if (server == NULL || out_packet == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "openvpn: server and packet output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_packet = NULL;
    ch_ovpn_session *session = ch_ovpn_session_for_server(server, error);
    if (session == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                               error->code;
    return ch_tunnel_stack_open_packet(session->tunnel, out_packet, error);
}

void ch_protocol_openvpn_reset(void) {
    (void)pthread_mutex_lock(&ch_ovpn_registry_mutex);
    ch_ovpn_session *sessions = ch_ovpn_registry;
    ch_ovpn_registry = NULL;
    (void)pthread_mutex_unlock(&ch_ovpn_registry_mutex);
    while (sessions != NULL) {
        ch_ovpn_session *next = sessions->next;
        sessions->next = NULL;
        ch_ovpn_session_destroy(sessions);
        sessions = next;
    }
}
