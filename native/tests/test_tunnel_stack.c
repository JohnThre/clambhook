// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <pthread.h>
#include <stdint.h>
#include <string.h>

#include "memory_testing.h"
#include "tunnel_stack.h"

#define CH_TUNNEL_TEST_REPEATS 128U
#define CH_TUNNEL_TEST_THREADS 4U
#define CH_TUNNEL_TEST_THREAD_REPEATS 32U

typedef struct ch_tunnel_test_thread_context {
    ch_tunnel_stack *stack;
    const uint8_t *packet;
    size_t length;
    int failed;
} ch_tunnel_test_thread_context;

static void ch_tunnel_test_discard_packet(const uint8_t *packet,
                                          size_t length, void *context) {
    (void)packet;
    (void)length;
    (void)context;
}

static void *ch_tunnel_test_inject_thread(void *opaque) {
    ch_tunnel_test_thread_context *context = opaque;
    for (size_t index = 0U; index < CH_TUNNEL_TEST_THREAD_REPEATS; ++index) {
        ch_error error;
        if (ch_tunnel_stack_inject(context->stack, context->packet,
                                   context->length, &error) != CH_OK) {
            context->failed = 1;
            break;
        }
    }
    return NULL;
}

void ch_test_tunnel_stack(void) {
    const char *addresses[] = {"10.23.0.1/24", "fd00:23::1/64"};
    ch_tunnel_stack_options options = {
        .addresses = addresses,
        .address_count = 2U,
        .mtu = 1420U,
        .packet_writer = ch_tunnel_test_discard_packet
    };
    ch_error error;
    ch_tunnel_stack *stack = ch_tunnel_stack_create(&options, &error);
    CH_TEST_ASSERT(stack != NULL);

    const uint8_t ipv4[] = {
        0x45U, 0x00U, 0x00U, 0x14U, 0x00U, 0x00U, 0x00U, 0x00U,
        0x40U, 0x3bU, 0x65U, 0xb2U, 0x0aU, 0x17U, 0x00U, 0x02U,
        0x0aU, 0x17U, 0x00U, 0x01U
    };
    const uint8_t ipv6[] = {
        0x60U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x3bU, 0x40U,
        0xfdU, 0x00U, 0x00U, 0x23U, 0x00U, 0x00U, 0x00U, 0x00U,
        0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x02U,
        0xfdU, 0x00U, 0x00U, 0x23U, 0x00U, 0x00U, 0x00U, 0x00U,
        0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x01U
    };
    uint8_t reusable[sizeof(ipv6)];
    for (size_t index = 0U; index < CH_TUNNEL_TEST_REPEATS; ++index) {
        const uint8_t *packet = index % 2U == 0U ? ipv4 : ipv6;
        size_t length = index % 2U == 0U ? sizeof(ipv4) : sizeof(ipv6);
        memcpy(reusable, packet, length);
        CH_TEST_ASSERT(ch_tunnel_stack_inject(stack, reusable, length,
                                               &error) == CH_OK);
        memset(reusable, 0xa5, length);
    }
    CH_TEST_ASSERT(ch_tunnel_stack_inject(stack, ipv4, 19U, &error) ==
                   CH_ERROR_INVALID_ARGUMENT);
    const uint8_t invalid_version[20] = {0x70U};
    CH_TEST_ASSERT(ch_tunnel_stack_inject(stack, invalid_version,
                                          sizeof(invalid_version), &error) ==
                   CH_ERROR_INVALID_ARGUMENT);

    pthread_t threads[CH_TUNNEL_TEST_THREADS];
    ch_tunnel_test_thread_context contexts[CH_TUNNEL_TEST_THREADS];
    for (size_t index = 0U; index < CH_TUNNEL_TEST_THREADS; ++index) {
        contexts[index] = (ch_tunnel_test_thread_context){
            .stack = stack,
            .packet = index % 2U == 0U ? ipv4 : ipv6,
            .length = index % 2U == 0U ? sizeof(ipv4) : sizeof(ipv6)
        };
        CH_TEST_ASSERT(pthread_create(&threads[index], NULL,
                                      ch_tunnel_test_inject_thread,
                                      &contexts[index]) == 0);
    }
    for (size_t index = 0U; index < CH_TUNNEL_TEST_THREADS; ++index) {
        CH_TEST_ASSERT(pthread_join(threads[index], NULL) == 0);
        CH_TEST_ASSERT(contexts[index].failed == 0);
    }

#ifdef CLAMBHOOK_MEMORY_TESTING
    size_t injected_packets = 0U;
    size_t payload_allocations = 0U;
    size_t copied_bytes = 0U;
    ch_tunnel_stack_memory_stats(stack, &injected_packets,
                                 &payload_allocations, &copied_bytes);
    CH_TEST_ASSERT(injected_packets == CH_TUNNEL_TEST_REPEATS +
        CH_TUNNEL_TEST_THREADS * CH_TUNNEL_TEST_THREAD_REPEATS);
    CH_TEST_ASSERT(payload_allocations == 0U);
    CH_TEST_ASSERT(copied_bytes == 0U);
    ch_tunnel_stack_stop_for_testing(stack);
    CH_TEST_ASSERT(ch_tunnel_stack_inject(stack, ipv4, sizeof(ipv4),
                                          &error) == CH_ERROR_INVALID_STATE);
    ch_tunnel_stack_memory_stats(stack, &injected_packets,
                                 &payload_allocations, &copied_bytes);
    CH_TEST_ASSERT(injected_packets == CH_TUNNEL_TEST_REPEATS +
        CH_TUNNEL_TEST_THREADS * CH_TUNNEL_TEST_THREAD_REPEATS);
    CH_TEST_ASSERT(payload_allocations == 0U);
    CH_TEST_ASSERT(copied_bytes == 0U);
#endif
    ch_tunnel_stack_destroy(stack);
}
