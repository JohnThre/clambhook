// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdint.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "conditioner.h"

static uint64_t conditioner_clock_nanoseconds(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value) != 0) return 0U;
    return (uint64_t)value.tv_sec * UINT64_C(1000000000) +
        (uint64_t)value.tv_nsec;
}

static void test_conditioner_config_snapshot(void) {
    static const char document[] =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.conditioner]\n"
        "enabled = true\n"
        "download_kbps = 512\n"
        "upload_kbps = 256\n"
        "latency = \"40ms\"\n"
        "jitter = \"10ms\"\n"
        "loss_percent = 2.5\n";
    ch_config *config = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    ch_conditioner_config snapshot;
    ch_conditioner_config_load(ch_config_active_profile(config), &snapshot);
    CH_TEST_ASSERT(snapshot.enabled == 1);
    CH_TEST_ASSERT(snapshot.download_bytes_per_second == UINT64_C(64000));
    CH_TEST_ASSERT(snapshot.upload_bytes_per_second == UINT64_C(32000));
    CH_TEST_ASSERT(snapshot.latency_nanoseconds == UINT64_C(40000000));
    CH_TEST_ASSERT(snapshot.jitter_nanoseconds == UINT64_C(10000000));
    CH_TEST_ASSERT(snapshot.loss_probability == 0.025);
    CH_TEST_ASSERT(ch_conditioner_active(&snapshot));
    ch_config_free(config);
}

static void test_conditioner_disabled_passthrough(void) {
    int descriptors[2] = {-1, -1};
    CH_TEST_ASSERT(socketpair(AF_UNIX, SOCK_STREAM, 0, descriptors) == 0);
    ch_conditioner_config config = {0};
    int wrapped = -1;
    ch_error error;
    CH_TEST_ASSERT(ch_conditioner_wrap_stream(
        descriptors[0], &config, &wrapped, &error) == CH_OK);
    CH_TEST_ASSERT(wrapped == descriptors[0]);
    (void)close(wrapped);
    (void)close(descriptors[1]);
}

static void test_conditioner_stream_latency(void) {
    int descriptors[2] = {-1, -1};
    CH_TEST_ASSERT(socketpair(AF_UNIX, SOCK_STREAM, 0, descriptors) == 0);
    struct timeval timeout = {.tv_sec = 1, .tv_usec = 0};
    CH_TEST_ASSERT(setsockopt(descriptors[1], SOL_SOCKET, SO_RCVTIMEO,
                              &timeout, sizeof(timeout)) == 0);
    ch_conditioner_config config = {
        .enabled = 1,
        .latency_nanoseconds = UINT64_C(20000000)
    };
    int wrapped = -1;
    ch_error error;
    CH_TEST_ASSERT(ch_conditioner_wrap_stream(
        descriptors[0], &config, &wrapped, &error) == CH_OK);
    CH_TEST_ASSERT(wrapped >= 0 && wrapped != descriptors[0]);

    uint64_t started = conditioner_clock_nanoseconds();
    CH_TEST_ASSERT(send(wrapped, "up", 2U, 0) == 2);
    char bytes[8] = {0};
    CH_TEST_ASSERT(recv(descriptors[1], bytes, sizeof(bytes), 0) == 2);
    CH_TEST_ASSERT_STRING("up", bytes);
    CH_TEST_ASSERT(conditioner_clock_nanoseconds() - started >=
                   UINT64_C(15000000));

    started = conditioner_clock_nanoseconds();
    CH_TEST_ASSERT(send(descriptors[1], "down", 4U, 0) == 4);
    memset(bytes, 0, sizeof(bytes));
    CH_TEST_ASSERT(recv(wrapped, bytes, sizeof(bytes), 0) == 4);
    CH_TEST_ASSERT_STRING("down", bytes);
    CH_TEST_ASSERT(conditioner_clock_nanoseconds() - started >=
                   UINT64_C(15000000));

    (void)shutdown(wrapped, SHUT_RDWR);
    (void)close(wrapped);
    (void)shutdown(descriptors[1], SHUT_RDWR);
    (void)close(descriptors[1]);
}

static void test_conditioner_packet_loss_contract(void) {
    ch_conditioner_bucket bucket = {0};
    ch_conditioner_config full_loss = {
        .enabled = 1,
        .loss_probability = 1.0
    };
    CH_TEST_ASSERT(ch_conditioner_before_upload(
        &full_loss, &bucket, 128U) == 1);
    ch_conditioner_config no_loss = {
        .enabled = 1,
        .latency_nanoseconds = 1U
    };
    CH_TEST_ASSERT(ch_conditioner_before_upload(
        &no_loss, &bucket, 128U) == 0);
    ch_conditioner_after_download(&no_loss, &bucket, 128U);
}

void ch_test_conditioner(void) {
    test_conditioner_config_snapshot();
    test_conditioner_disabled_passthrough();
    test_conditioner_stream_latency();
    test_conditioner_packet_loss_contract();
}
