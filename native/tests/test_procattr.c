// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

#include "clambhook/procattr.h"

static void procattr_test_source_parsing(void) {
    int port = 0;
    CH_TEST_ASSERT(ch_procattr_local_port("127.0.0.1:54321", &port));
    CH_TEST_ASSERT(port == 54321);
    CH_TEST_ASSERT(ch_procattr_local_port("[::1]:8080", &port));
    CH_TEST_ASSERT(port == 8080);
    CH_TEST_ASSERT(ch_procattr_local_port(" 127.0.0.1:443 ", &port));
    CH_TEST_ASSERT(port == 443);
    CH_TEST_ASSERT(!ch_procattr_local_port("127.0.0.1", &port));
    CH_TEST_ASSERT(!ch_procattr_local_port("2001:db8::1:443", &port));
    CH_TEST_ASSERT(!ch_procattr_local_port("[::1]443", &port));
    CH_TEST_ASSERT(!ch_procattr_local_port("127.0.0.1:0", &port));
    CH_TEST_ASSERT(!ch_procattr_local_port("127.0.0.1:70000", &port));
    CH_TEST_ASSERT(!ch_procattr_local_port("127.0.0.1:not-a-port", &port));
}

static void procattr_test_live_tcp(void) {
    int listener = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (listener < 0) return;
    struct sockaddr_in address;
    memset(&address, 0, sizeof(address));
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    address.sin_port = 0;
    if (bind(listener, (struct sockaddr *)&address, sizeof(address)) != 0 ||
        listen(listener, 1) != 0) {
        (void)close(listener);
        return;
    }
    socklen_t length = (socklen_t)sizeof(address);
    if (getsockname(listener, (struct sockaddr *)&address, &length) != 0) {
        (void)close(listener);
        return;
    }
    int client = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (client < 0 ||
        connect(client, (struct sockaddr *)&address, sizeof(address)) != 0) {
        if (client >= 0) (void)close(client);
        (void)close(listener);
        return;
    }
    int accepted = accept(listener, NULL, NULL);
    struct sockaddr_in source;
    memset(&source, 0, sizeof(source));
    length = (socklen_t)sizeof(source);
    if (accepted >= 0 &&
        getsockname(client, (struct sockaddr *)&source, &length) == 0) {
        char endpoint[64];
        (void)snprintf(endpoint, sizeof(endpoint), "127.0.0.1:%u",
                       (unsigned int)ntohs(source.sin_port));
        ch_process_info process = {0};
        if (ch_procattr_lookup("tcp", endpoint, &process)) {
            int own_process = process.pid == (int)getpid();
            int has_name = process.name != NULL && process.name[0] != '\0';
            int has_path = process.path != NULL && process.path[0] != '\0';
            ch_process_info_clear(&process);
            CH_TEST_ASSERT(own_process);
            CH_TEST_ASSERT(has_name);
            CH_TEST_ASSERT(has_path);
        }
    }
    if (accepted >= 0) (void)close(accepted);
    (void)close(client);
    (void)close(listener);
}

static void procattr_test_live_udp(void) {
    int descriptor = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (descriptor < 0) return;
    struct sockaddr_in address;
    memset(&address, 0, sizeof(address));
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    address.sin_port = 0;
    socklen_t length = (socklen_t)sizeof(address);
    if (bind(descriptor, (struct sockaddr *)&address, sizeof(address)) == 0 &&
        getsockname(descriptor, (struct sockaddr *)&address, &length) == 0) {
        char endpoint[64];
        (void)snprintf(endpoint, sizeof(endpoint), "127.0.0.1:%u",
                       (unsigned int)ntohs(address.sin_port));
        ch_process_info process = {0};
        if (ch_procattr_lookup("udp", endpoint, &process)) {
            int own_process = process.pid == (int)getpid();
            int has_name = process.name != NULL && process.name[0] != '\0';
            int has_path = process.path != NULL && process.path[0] != '\0';
            ch_process_info_clear(&process);
            CH_TEST_ASSERT(own_process);
            CH_TEST_ASSERT(has_name);
            CH_TEST_ASSERT(has_path);
        }
    }
    (void)close(descriptor);
}

void ch_test_procattr(void) {
    procattr_test_source_parsing();
    procattr_test_live_tcp();
    procattr_test_live_udp();
}
