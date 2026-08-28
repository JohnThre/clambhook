// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <arpa/inet.h>
#include <errno.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/listener.h"
#include "clambhook/protocol.h"

typedef struct listener_dial_context {
    const char *expected_target;
    ch_proxy_route_action action;
    atomic_int matched;
    pthread_t echo_thread;
    int echo_started;
    const ch_config_table *packet_chain;
} listener_dial_context;

typedef struct listener_udp_echo {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    int success;
} listener_udp_echo;

static int test_send_all(int descriptor, const void *bytes, size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = send(descriptor, cursor, length, 0);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int test_receive_exact(int descriptor, void *bytes, size_t length) {
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

static void *listener_echo_main(void *context) {
    int descriptor = *(int *)context;
    free(context);
    uint8_t bytes[4096];
    for (;;) {
        ssize_t received = recv(descriptor, bytes, sizeof(bytes), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0 || !test_send_all(descriptor, bytes, (size_t)received)) break;
    }
    (void)close(descriptor);
    return NULL;
}

static ch_status listener_test_dial(const char *network, const char *target,
                                    const char *source, ch_proxy_route *route,
                                    int *out_descriptor, void *context,
                                    ch_error *error) {
    listener_dial_context *dial = context;
    (void)error;
    if (strcmp("tcp", network) != 0 || source == NULL || source[0] == '\0') {
        ++ch_test_failures;
        return CH_ERROR_INTERNAL;
    }
    atomic_store(&dial->matched,
                 strcmp(target, dial->expected_target) == 0 ? 1 : -1);
    route->action = dial->action;
    (void)snprintf(route->session_key, sizeof(route->session_key),
                   "listener-test-direct");
    *out_descriptor = -1;
    if (dial->action != CH_PROXY_ROUTE_CONNECT) return CH_OK;
    int descriptors[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, descriptors) != 0) return CH_ERROR_IO;
    int *echo_descriptor = malloc(sizeof(*echo_descriptor));
    if (echo_descriptor == NULL) {
        (void)close(descriptors[0]);
        (void)close(descriptors[1]);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *echo_descriptor = descriptors[1];
    if (pthread_create(&dial->echo_thread, NULL, listener_echo_main,
                       echo_descriptor) != 0) {
        free(echo_descriptor);
        (void)close(descriptors[0]);
        (void)close(descriptors[1]);
        return CH_ERROR_INTERNAL;
    }
    dial->echo_started = 1;
    *out_descriptor = descriptors[0];
    return CH_OK;
}

static ch_status listener_test_packet_dial(
    const char *network,
    const char *target,
    const char *source,
    ch_proxy_route *route,
    ch_packet_connection **out_connection,
    void *context,
    ch_error *error) {
    listener_dial_context *dial = context;
    if (strcmp("udp", network) != 0 || source == NULL || source[0] == '\0') {
        return CH_ERROR_INTERNAL;
    }
    atomic_store(&dial->matched,
                 strcmp(target, dial->expected_target) == 0 ? 1 : -1);
    route->action = dial->action;
    *out_connection = NULL;
    if (dial->action != CH_PROXY_ROUTE_CONNECT) return CH_OK;
    return ch_protocol_chain_dial_packet(dial->packet_chain, target,
                                         out_connection, error);
}

static void *listener_udp_echo_main(void *opaque) {
    listener_udp_echo *echo = opaque;
    for (unsigned int packet_index = 0U; packet_index < 2U; ++packet_index) {
        uint8_t packet[1024];
        struct sockaddr_storage source;
        socklen_t source_length = (socklen_t)sizeof(source);
        ssize_t length;
        do {
            length = recvfrom(echo->descriptor, packet, sizeof(packet), 0,
                              (struct sockaddr *)&source, &source_length);
        } while (length < 0 && errno == EINTR);
        if (length < 0) return NULL;
        ssize_t sent;
        do {
            sent = sendto(echo->descriptor, packet, (size_t)length, 0,
                          (struct sockaddr *)&source, source_length);
        } while (sent < 0 && errno == EINTR);
        if (sent != length) return NULL;
    }
    echo->success = 1;
    return NULL;
}

static int listener_udp_echo_start(listener_udp_echo *echo) {
    memset(echo, 0, sizeof(*echo));
    echo->descriptor = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (echo->descriptor < 0) return 0;
    struct sockaddr_in address = {
        .sin_family = AF_INET,
        .sin_port = 0,
        .sin_addr = {.s_addr = htonl(INADDR_LOOPBACK)}
    };
    if (bind(echo->descriptor, (struct sockaddr *)&address,
             (socklen_t)sizeof(address)) != 0) return 0;
    socklen_t length = (socklen_t)sizeof(address);
    if (getsockname(echo->descriptor, (struct sockaddr *)&address,
                    &length) != 0) return 0;
    echo->port = ntohs(address.sin_port);
    return pthread_create(&echo->thread, NULL, listener_udp_echo_main,
                          echo) == 0;
}

static void listener_udp_echo_stop(listener_udp_echo *echo) {
    (void)pthread_join(echo->thread, NULL);
    (void)close(echo->descriptor);
}

static int listener_connect(const char *address) {
    const char *colon = strrchr(address, ':');
    if (colon == NULL) return -1;
    char host[64];
    size_t host_length = (size_t)(colon - address);
    if (host_length == 0U || host_length >= sizeof(host)) return -1;
    memcpy(host, address, host_length);
    host[host_length] = '\0';
    unsigned long port = strtoul(colon + 1, NULL, 10);
    if (port == 0UL || port > 65535UL) return -1;
    int descriptor = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (descriptor < 0) return -1;
    struct sockaddr_in target = {
        .sin_family = AF_INET,
        .sin_port = htons((uint16_t)port)
    };
    if (inet_pton(AF_INET, host, &target.sin_addr) != 1 ||
        connect(descriptor, (struct sockaddr *)&target,
                (socklen_t)sizeof(target)) != 0) {
        (void)close(descriptor);
        return -1;
    }
    return descriptor;
}

static size_t listener_receive_header(int descriptor, char *buffer,
                                      size_t capacity) {
    size_t length = 0U;
    if (capacity > 0U) buffer[0] = '\0';
    while (length + 1U < capacity) {
        if (!test_receive_exact(descriptor, buffer + length, 1U)) return 0U;
        ++length;
        buffer[length] = '\0';
        if (length >= 4U && strcmp(buffer + length - 4U, "\r\n\r\n") == 0) break;
    }
    return length;
}

static void listener_join_echo(listener_dial_context *dial) {
    if (dial->echo_started) {
        (void)pthread_join(dial->echo_thread, NULL);
        dial->echo_started = 0;
    }
}

static void test_socks5_connect_and_auth(void) {
    listener_dial_context dial = {
        .expected_target = "example.com:443",
        .action = CH_PROXY_ROUTE_CONNECT
    };
    ch_proxy_listener_options options = {
        .protocol = CH_PROXY_LISTENER_SOCKS5,
        .address = "127.0.0.1:0",
        .authentication_required = 1,
        .username = "user",
        .password = "pass",
        .maximum_connections = 4U,
        .handshake_timeout_milliseconds = 2000U,
        .dial = listener_test_dial,
        .dial_context = &dial
    };
    ch_error error;
    ch_proxy_listener *listener = ch_proxy_listener_start(&options, &error);
    if (listener == NULL) fprintf(stderr, "socks listener: %s\n", error.message);
    CH_TEST_ASSERT(listener != NULL);
    CH_TEST_ASSERT_STRING("socks5", ch_proxy_listener_protocol_name(listener));
    int client = listener_connect(ch_proxy_listener_address(listener));
    CH_TEST_ASSERT(client >= 0);
    const uint8_t greeting[] = {0x05U, 0x01U, 0x02U};
    uint8_t response[10];
    CH_TEST_ASSERT(test_send_all(client, greeting, sizeof(greeting)));
    CH_TEST_ASSERT(test_receive_exact(client, response, 2U));
    CH_TEST_ASSERT(response[0] == 0x05U && response[1] == 0x02U);
    const uint8_t auth[] = {0x01U, 0x04U, 'u', 's', 'e', 'r',
                            0x04U, 'p', 'a', 's', 's'};
    CH_TEST_ASSERT(test_send_all(client, auth, sizeof(auth)));
    CH_TEST_ASSERT(test_receive_exact(client, response, 2U));
    CH_TEST_ASSERT(response[0] == 0x01U && response[1] == 0x00U);
    const uint8_t request[] = {0x05U, 0x01U, 0x00U, 0x03U, 0x0bU,
        'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 0x01U, 0xbbU};
    CH_TEST_ASSERT(test_send_all(client, request, sizeof(request)));
    CH_TEST_ASSERT(test_receive_exact(client, response, sizeof(response)));
    CH_TEST_ASSERT(response[1] == 0x00U);
    static const char payload[] = "native-socks";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(test_send_all(client, payload, sizeof(payload)));
    CH_TEST_ASSERT(test_receive_exact(client, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    CH_TEST_ASSERT(atomic_load(&dial.matched) == 1);
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    ch_proxy_listener_stop(listener);
    listener_join_echo(&dial);
}

static void test_socks5_block(void) {
    listener_dial_context dial = {
        .expected_target = "blocked.example:80",
        .action = CH_PROXY_ROUTE_BLOCK
    };
    ch_proxy_listener_options options = {
        .protocol = CH_PROXY_LISTENER_SOCKS5,
        .address = "127.0.0.1:0",
        .dial = listener_test_dial,
        .dial_context = &dial
    };
    ch_error error;
    ch_proxy_listener *listener = ch_proxy_listener_start(&options, &error);
    if (listener == NULL) fprintf(stderr, "blocked listener: %s\n", error.message);
    CH_TEST_ASSERT(listener != NULL);
    int client = listener_connect(ch_proxy_listener_address(listener));
    CH_TEST_ASSERT(client >= 0);
    const uint8_t greeting[] = {0x05U, 0x01U, 0x00U};
    uint8_t response[10];
    CH_TEST_ASSERT(test_send_all(client, greeting, sizeof(greeting)));
    CH_TEST_ASSERT(test_receive_exact(client, response, 2U));
    const uint8_t request[] = {0x05U, 0x01U, 0x00U, 0x03U, 0x0fU,
        'b', 'l', 'o', 'c', 'k', 'e', 'd', '.', 'e', 'x', 'a', 'm', 'p', 'l', 'e',
        0x00U, 0x50U};
    CH_TEST_ASSERT(test_send_all(client, request, sizeof(request)));
    CH_TEST_ASSERT(test_receive_exact(client, response, sizeof(response)));
    CH_TEST_ASSERT(response[1] == 0x02U);
    CH_TEST_ASSERT(atomic_load(&dial.matched) == 1);
    (void)close(client);
    ch_proxy_listener_stop(listener);
}

static void test_socks5_udp_associate(void) {
    listener_udp_echo echo;
    CH_TEST_ASSERT(listener_udp_echo_start(&echo));
    static const char document[] =
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"direct\"\n"
        "[[profile.chain.server]]\nprotocol = \"direct\"\n";
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    char target[64];
    (void)snprintf(target, sizeof(target), "127.0.0.1:%u",
                   (unsigned int)echo.port);
    listener_dial_context dial = {
        .expected_target = target,
        .action = CH_PROXY_ROUTE_CONNECT,
        .packet_chain = ch_config_array_get_table(chains, 0U)
    };
    ch_proxy_listener_options options = {
        .protocol = CH_PROXY_LISTENER_SOCKS5,
        .address = "127.0.0.1:0",
        .dial = listener_test_dial,
        .packet_dial = listener_test_packet_dial,
        .dial_context = &dial
    };
    ch_proxy_listener *listener = ch_proxy_listener_start(&options, &error);
    CH_TEST_ASSERT(listener != NULL);
    int control = listener_connect(ch_proxy_listener_address(listener));
    CH_TEST_ASSERT(control >= 0);
    const uint8_t greeting[] = {0x05U, 0x01U, 0x00U};
    uint8_t reply[22];
    CH_TEST_ASSERT(test_send_all(control, greeting, sizeof(greeting)));
    CH_TEST_ASSERT(test_receive_exact(control, reply, 2U));
    CH_TEST_ASSERT(reply[1] == 0x00U);
    const uint8_t associate[] = {
        0x05U, 0x03U, 0x00U, 0x01U, 0U, 0U, 0U, 0U, 0U, 0U
    };
    CH_TEST_ASSERT(test_send_all(control, associate, sizeof(associate)));
    CH_TEST_ASSERT(test_receive_exact(control, reply, 10U));
    CH_TEST_ASSERT(reply[0] == 0x05U && reply[1] == 0x00U &&
                   reply[3] == 0x01U);
    struct sockaddr_in relay = {
        .sin_family = AF_INET
    };
    memcpy(&relay.sin_addr, reply + 4U, 4U);
    memcpy(&relay.sin_port, reply + 8U, 2U);
    int udp = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    CH_TEST_ASSERT(udp >= 0);
    static const uint8_t payload[] = "socks5-native-udp";
    uint8_t request[10U + sizeof(payload)];
    request[0] = 0U;
    request[1] = 0U;
    request[2] = 0U;
    request[3] = 0x01U;
    request[4] = 127U;
    request[5] = 0U;
    request[6] = 0U;
    request[7] = 1U;
    request[8] = (uint8_t)(echo.port >> 8U);
    request[9] = (uint8_t)echo.port;
    memcpy(request + 10U, payload, sizeof(payload));
    for (unsigned int packet_index = 0U; packet_index < 2U; ++packet_index) {
        CH_TEST_ASSERT(sendto(
            udp, request, sizeof(request), 0, (struct sockaddr *)&relay,
            (socklen_t)sizeof(relay)) == (ssize_t)sizeof(request));
        uint8_t response[256];
        struct sockaddr_storage response_source;
        socklen_t response_source_length =
            (socklen_t)sizeof(response_source);
        ssize_t response_length;
        do {
            response_length = recvfrom(
                udp, response, sizeof(response), 0,
                (struct sockaddr *)&response_source,
                &response_source_length);
        } while (response_length < 0 && errno == EINTR);
        CH_TEST_ASSERT(response_length == (ssize_t)sizeof(request));
        CH_TEST_ASSERT(response[0] == 0U && response[1] == 0U &&
                       response[2] == 0U && response[3] == 0x01U &&
                       response[4] == 127U && response[5] == 0U &&
                       response[6] == 0U && response[7] == 1U &&
                       response[8] == (uint8_t)(echo.port >> 8U) &&
                       response[9] == (uint8_t)echo.port);
        CH_TEST_ASSERT(memcmp(response + 10U, payload,
                              sizeof(payload)) == 0);
    }
    CH_TEST_ASSERT(atomic_load(&dial.matched) == 1);
    (void)close(udp);
    (void)shutdown(control, SHUT_RDWR);
    (void)close(control);
    ch_proxy_listener_stop(listener);
    listener_udp_echo_stop(&echo);
    CH_TEST_ASSERT(echo.success == 1);
    ch_config_free(config);
}

static void test_http_connect_and_forward(void) {
    listener_dial_context dial = {
        .expected_target = "example.com:443",
        .action = CH_PROXY_ROUTE_CONNECT
    };
    ch_proxy_listener_options options = {
        .protocol = CH_PROXY_LISTENER_HTTP,
        .address = "127.0.0.1:0",
        .authentication_required = 1,
        .username = "user",
        .password = "pass",
        .dial = listener_test_dial,
        .dial_context = &dial
    };
    ch_error error;
    ch_proxy_listener *listener = ch_proxy_listener_start(&options, &error);
    if (listener == NULL) fprintf(stderr, "http listener: %s\n", error.message);
    CH_TEST_ASSERT(listener != NULL);
    CH_TEST_ASSERT_STRING("http", ch_proxy_listener_protocol_name(listener));
    int client = listener_connect(ch_proxy_listener_address(listener));
    CH_TEST_ASSERT(client >= 0);
    static const char connect_request[] =
        "CONNECT example.com:443 HTTP/1.1\r\n"
        "Host: example.com:443\r\n"
        "Proxy-Authorization: Basic dXNlcjpwYXNz\r\n\r\n";
    CH_TEST_ASSERT(test_send_all(client, connect_request, sizeof(connect_request) - 1U));
    char header[1024];
    (void)listener_receive_header(client, header, sizeof(header));
    CH_TEST_ASSERT(strstr(header, "200 Connection established") != NULL);
    static const char payload[] = "native-http";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(test_send_all(client, payload, sizeof(payload)));
    CH_TEST_ASSERT(test_receive_exact(client, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    ch_proxy_listener_stop(listener);
    listener_join_echo(&dial);

    dial = (listener_dial_context){
        .expected_target = "example.com:80",
        .action = CH_PROXY_ROUTE_CONNECT
    };
    options.authentication_required = 0;
    options.dial_context = &dial;
    listener = ch_proxy_listener_start(&options, &error);
    CH_TEST_ASSERT(listener != NULL);
    client = listener_connect(ch_proxy_listener_address(listener));
    CH_TEST_ASSERT(client >= 0);
    static const char forward_request[] =
        "GET http://example.com/path?q=1 HTTP/1.1\r\n"
        "Host: example.com\r\nProxy-Connection: keep-alive\r\n\r\n";
    CH_TEST_ASSERT(test_send_all(client, forward_request, sizeof(forward_request) - 1U));
    (void)listener_receive_header(client, header, sizeof(header));
    CH_TEST_ASSERT(strstr(header, "GET /path?q=1 HTTP/1.1\r\n") == header);
    CH_TEST_ASSERT(strstr(header, "Host: example.com\r\n") != NULL);
    CH_TEST_ASSERT(strstr(header, "Proxy-Connection") == NULL);
    CH_TEST_ASSERT(atomic_load(&dial.matched) == 1);
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    ch_proxy_listener_stop(listener);
    listener_join_echo(&dial);
}

void ch_test_listener(void) {
    test_socks5_connect_and_auth();
    test_socks5_block();
    test_socks5_udp_associate();
    test_http_connect_and_forward();
}
