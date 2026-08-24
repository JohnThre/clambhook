#include "test.h"

#include <arpa/inet.h>
#include <errno.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/json.h"
#include "clambhook/runtime.h"

typedef struct runtime_echo_server {
    int descriptor;
    pthread_t thread;
    uint16_t port;
} runtime_echo_server;

static int runtime_test_send_all(int descriptor, const void *bytes, size_t length) {
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

static int runtime_test_receive_exact(int descriptor, void *bytes, size_t length) {
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

static void *runtime_echo_main(void *context) {
    runtime_echo_server *server = context;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client >= 0) {
        uint8_t bytes[256];
        ssize_t received;
        do {
            received = recv(client, bytes, sizeof(bytes), 0);
        } while (received < 0 && errno == EINTR);
        if (received > 0) {
            (void)runtime_test_send_all(client, bytes, (size_t)received);
        }
        (void)close(client);
    }
    return NULL;
}

static int runtime_echo_start(runtime_echo_server *server) {
    memset(server, 0, sizeof(*server));
    server->descriptor = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (server->descriptor < 0) return 0;
    struct sockaddr_in address = {
        .sin_family = AF_INET,
        .sin_port = 0,
        .sin_addr = {.s_addr = htonl(INADDR_LOOPBACK)}
    };
    if (bind(server->descriptor, (struct sockaddr *)&address,
             (socklen_t)sizeof(address)) != 0 || listen(server->descriptor, 1) != 0) {
        (void)close(server->descriptor);
        return 0;
    }
    socklen_t length = (socklen_t)sizeof(address);
    if (getsockname(server->descriptor, (struct sockaddr *)&address, &length) != 0) {
        (void)close(server->descriptor);
        return 0;
    }
    server->port = ntohs(address.sin_port);
    if (pthread_create(&server->thread, NULL, runtime_echo_main, server) != 0) {
        (void)close(server->descriptor);
        return 0;
    }
    return 1;
}

static void runtime_echo_stop(runtime_echo_server *server) {
    (void)pthread_join(server->thread, NULL);
    (void)close(server->descriptor);
}

static int runtime_connect_address(const char *address) {
    const char *separator = strrchr(address, ':');
    if (separator == NULL || separator == address) return -1;
    char host[64];
    size_t host_length = (size_t)(separator - address);
    if (host_length >= sizeof(host)) return -1;
    memcpy(host, address, host_length);
    host[host_length] = '\0';
    unsigned long port = strtoul(separator + 1, NULL, 10);
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

void ch_test_runtime_listener(void) {
    runtime_echo_server echo;
    CH_TEST_ASSERT(runtime_echo_start(&echo));
    char config_path[160];
    (void)snprintf(config_path, sizeof(config_path),
                   "/tmp/clambhook-runtime-listener-%ld.toml", (long)getpid());
    FILE *file = fopen(config_path, "wb");
    CH_TEST_ASSERT(file != NULL);
    CH_TEST_ASSERT(fputs(
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[profile.listen]\nsocks5 = \"127.0.0.1:0\"\n"
        "[[profile.chain]]\nname = \"default\"\n"
        "[[profile.chain.server]]\nprotocol = \"direct\"\n",
        file) >= 0);
    CH_TEST_ASSERT(fclose(file) == 0);
    ch_error error;
    ch_runtime *runtime = ch_runtime_create(NULL, &error);
    CH_TEST_ASSERT(runtime != NULL);
    CH_TEST_ASSERT(ch_runtime_start(runtime, config_path, &error) == CH_OK);
    char *status_json = NULL;
    CH_TEST_ASSERT(ch_runtime_query(runtime, "status", "{}", &status_json, &error) == CH_OK);
    ch_json_value *status = ch_json_parse(status_json, strlen(status_json), &error);
    CH_TEST_ASSERT(status != NULL);
    const ch_json_value *listeners = ch_json_object_get(status, "listeners");
    CH_TEST_ASSERT(ch_json_array_size(listeners) == 1U);
    const char *listener_address = ch_json_string_value(
        ch_json_object_get(ch_json_array_get(listeners, 0U), "addr")
    );
    CH_TEST_ASSERT(listener_address != NULL);
    int client = runtime_connect_address(listener_address);
    CH_TEST_ASSERT(client >= 0);
    ch_json_value_destroy(status);
    ch_string_free(status_json);

    const uint8_t greeting[] = {0x05U, 0x01U, 0x00U};
    uint8_t response[10];
    CH_TEST_ASSERT(runtime_test_send_all(client, greeting, sizeof(greeting)));
    CH_TEST_ASSERT(runtime_test_receive_exact(client, response, 2U));
    CH_TEST_ASSERT(response[1] == 0x00U);
    uint8_t request[] = {0x05U, 0x01U, 0x00U, 0x01U,
                         127U, 0U, 0U, 1U,
                         (uint8_t)(echo.port >> 8U), (uint8_t)echo.port};
    CH_TEST_ASSERT(runtime_test_send_all(client, request, sizeof(request)));
    CH_TEST_ASSERT(runtime_test_receive_exact(client, response, sizeof(response)));
    CH_TEST_ASSERT(response[1] == 0x00U);
    static const char payload[] = "runtime-direct-chain";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(runtime_test_send_all(client, payload, sizeof(payload)));
    CH_TEST_ASSERT(runtime_test_receive_exact(client, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    CH_TEST_ASSERT(ch_runtime_stop(runtime, &error) == CH_OK);
    ch_runtime_destroy(runtime);
    runtime_echo_stop(&echo);
    CH_TEST_ASSERT(unlink(config_path) == 0);
}
