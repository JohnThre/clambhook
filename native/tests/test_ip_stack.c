#include "test.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/ip_stack.h"
#include "internal.h"

typedef struct ip_stack_test_output {
    uint8_t packet[2048];
    size_t length;
    unsigned int count;
} ip_stack_test_output;

typedef struct ip_stack_test_dial {
    int peer;
    char target[64];
    char source[64];
    unsigned int count;
} ip_stack_test_dial;

static uint32_t ip_stack_test_sum(const uint8_t *bytes, size_t length,
                                  uint32_t sum) {
    while (length >= 2U) {
        sum += ((uint32_t)bytes[0] << 8U) | bytes[1];
        bytes += 2U;
        length -= 2U;
    }
    if (length > 0U) sum += (uint32_t)bytes[0] << 8U;
    return sum;
}

static uint16_t ip_stack_test_finish_sum(uint32_t sum) {
    while ((sum >> 16U) != 0U) sum = (sum & 0xffffU) + (sum >> 16U);
    return (uint16_t)~sum;
}

static uint16_t ip_stack_test_checksum(const uint8_t *bytes, size_t length) {
    return ip_stack_test_finish_sum(ip_stack_test_sum(bytes, length, 0U));
}

static void ip_stack_test_set_checksum(uint8_t *bytes, size_t offset,
                                       uint16_t checksum) {
    bytes[offset] = (uint8_t)(checksum >> 8U);
    bytes[offset + 1U] = (uint8_t)checksum;
}

static uint32_t ip_stack_test_read_u32(const uint8_t *bytes) {
    return ((uint32_t)bytes[0] << 24U) | ((uint32_t)bytes[1] << 16U) |
        ((uint32_t)bytes[2] << 8U) | bytes[3];
}

static void ip_stack_test_write_u16(uint8_t *bytes, uint16_t value) {
    bytes[0] = (uint8_t)(value >> 8U);
    bytes[1] = (uint8_t)value;
}

static void ip_stack_test_write_u32(uint8_t *bytes, uint32_t value) {
    bytes[0] = (uint8_t)(value >> 24U);
    bytes[1] = (uint8_t)(value >> 16U);
    bytes[2] = (uint8_t)(value >> 8U);
    bytes[3] = (uint8_t)value;
}

static uint16_t ip_stack_test_tcp_checksum(const uint8_t *packet,
                                           size_t length) {
    size_t tcp_length = length - 20U;
    uint32_t sum = ip_stack_test_sum(packet + 12U, 8U, 0U);
    uint8_t pseudo[] = {
        0U, 6U, (uint8_t)(tcp_length >> 8U), (uint8_t)tcp_length
    };
    sum = ip_stack_test_sum(pseudo, sizeof(pseudo), sum);
    sum = ip_stack_test_sum(packet + 20U, tcp_length, sum);
    return ip_stack_test_finish_sum(sum);
}

static size_t ip_stack_test_tcp_packet(
    uint8_t *packet, size_t capacity, const uint8_t source[4],
    const uint8_t target[4], uint16_t source_port, uint16_t target_port,
    uint32_t sequence, uint32_t acknowledgement, uint8_t flags,
    const uint8_t *payload, size_t payload_length) {
    size_t length = 40U + payload_length;
    if (capacity < length || length > UINT16_MAX) return 0U;
    memset(packet, 0, length);
    packet[0] = 0x45U;
    ip_stack_test_write_u16(packet + 2U, (uint16_t)length);
    packet[4] = 0x44U;
    packet[5] = 0x21U;
    packet[8] = 64U;
    packet[9] = 6U;
    memcpy(packet + 12U, source, 4U);
    memcpy(packet + 16U, target, 4U);
    ip_stack_test_write_u16(packet + 20U, source_port);
    ip_stack_test_write_u16(packet + 22U, target_port);
    ip_stack_test_write_u32(packet + 24U, sequence);
    ip_stack_test_write_u32(packet + 28U, acknowledgement);
    packet[32] = 0x50U;
    packet[33] = flags;
    packet[34] = 0xffU;
    packet[35] = 0xffU;
    if (payload_length > 0U) memcpy(packet + 40U, payload, payload_length);
    ip_stack_test_set_checksum(packet, 10U,
                               ip_stack_test_checksum(packet, 20U));
    ip_stack_test_set_checksum(packet + 20U, 16U,
                               ip_stack_test_tcp_checksum(packet, length));
    return length;
}

static ch_status ip_stack_test_tcp_dialer(
    const char *target, const char *source, int *out_descriptor,
    void *context, ch_error *error) {
    ip_stack_test_dial *dial = context;
    int descriptors[2];
    ch_error_clear(error);
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, descriptors) != 0) {
        ch_error_set(error, CH_ERROR_IO, "create TCP test socketpair");
        return CH_ERROR_IO;
    }
    ++dial->count;
    (void)snprintf(dial->target, sizeof(dial->target), "%s", target);
    (void)snprintf(dial->source, sizeof(dial->source), "%s", source);
    dial->peer = descriptors[1];
    *out_descriptor = descriptors[0];
    return CH_OK;
}

static void ip_stack_test_writer(const uint8_t *packet, size_t length,
                                 void *context) {
    ip_stack_test_output *output = context;
    ++output->count;
    output->length = length > sizeof(output->packet) ? sizeof(output->packet) :
                                                      length;
    memcpy(output->packet, packet, output->length);
}

static void ip_stack_test_ipv4_echo(ch_ip_stack *stack,
                                    ip_stack_test_output *output) {
    uint8_t packet[32] = {
        0x45U, 0x00U, 0x00U, 0x20U, 0x12U, 0x34U, 0x00U, 0x00U,
        64U, 1U, 0U, 0U, 198U, 18U, 0U, 2U,
        198U, 18U, 0U, 1U,
        8U, 0U, 0U, 0U, 0xabU, 0xcdU, 0U, 1U,
        'p', 'i', 'n', 'g'
    };
    ip_stack_test_set_checksum(packet, 10U,
                               ip_stack_test_checksum(packet, 20U));
    ip_stack_test_set_checksum(packet + 20U, 2U,
                               ip_stack_test_checksum(packet + 20U, 12U));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, sizeof(packet),
                                      &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length == sizeof(packet));
    CH_TEST_ASSERT(output->packet[0] == 0x45U);
    CH_TEST_ASSERT(output->packet[9] == 1U);
    CH_TEST_ASSERT(memcmp(output->packet + 12U,
                          "\306\022\000\001\306\022\000\002", 8U) == 0);
    CH_TEST_ASSERT(output->packet[20] == 0U);
    CH_TEST_ASSERT(ip_stack_test_checksum(output->packet, 20U) == 0U);
    CH_TEST_ASSERT(ip_stack_test_checksum(output->packet + 20U, 12U) == 0U);
}

static uint16_t ip_stack_test_icmp6_checksum(const uint8_t *packet,
                                             size_t length) {
    size_t payload_length = length - 40U;
    uint32_t sum = ip_stack_test_sum(packet + 8U, 32U, 0U);
    uint8_t pseudo_tail[8] = {
        (uint8_t)(payload_length >> 24U),
        (uint8_t)(payload_length >> 16U),
        (uint8_t)(payload_length >> 8U),
        (uint8_t)payload_length,
        0U, 0U, 0U, 58U
    };
    sum = ip_stack_test_sum(pseudo_tail, sizeof(pseudo_tail), sum);
    sum = ip_stack_test_sum(packet + 40U, payload_length, sum);
    return ip_stack_test_finish_sum(sum);
}

static void ip_stack_test_ipv6_echo(ch_ip_stack *stack,
                                    ip_stack_test_output *output) {
    memset(output, 0, sizeof(*output));
    uint8_t packet[52] = {
        0x60U, 0U, 0U, 0U, 0U, 12U, 58U, 64U,
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 2U,
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 1U,
        128U, 0U, 0U, 0U, 0x12U, 0x34U, 0U, 1U,
        'p', 'o', 'n', 'g'
    };
    ip_stack_test_set_checksum(packet + 40U, 2U,
                               ip_stack_test_icmp6_checksum(packet,
                                                            sizeof(packet)));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, sizeof(packet),
                                      &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length == sizeof(packet));
    CH_TEST_ASSERT((output->packet[0] >> 4U) == 6U);
    CH_TEST_ASSERT(output->packet[6] == 58U);
    CH_TEST_ASSERT(output->packet[40] == 129U);
    CH_TEST_ASSERT(output->packet[23] == 1U && output->packet[39] == 2U);
    CH_TEST_ASSERT(ip_stack_test_icmp6_checksum(
        output->packet, output->length) == 0U);
}

static void ip_stack_test_ipv4_tcp(ch_ip_stack *stack,
                                   ip_stack_test_output *output,
                                   ip_stack_test_dial *dial) {
    static const uint8_t client_address[] = {198U, 18U, 0U, 2U};
    static const uint8_t target_address[] = {203U, 0U, 113U, 9U};
    uint8_t packet[256];
    size_t length = ip_stack_test_tcp_packet(
        packet, sizeof(packet), client_address, target_address,
        40000U, 443U, 100U, 0U, 0x02U, NULL, 0U);
    CH_TEST_ASSERT(length == 40U);
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length >= 40U);
    CH_TEST_ASSERT(memcmp(output->packet + 12U, target_address, 4U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 16U, client_address, 4U) == 0);
    CH_TEST_ASSERT(output->packet[20] == 0x01U &&
                   output->packet[21] == 0xbbU);
    CH_TEST_ASSERT(output->packet[22] == 0x9cU &&
                   output->packet[23] == 0x40U);
    CH_TEST_ASSERT((output->packet[33] & 0x12U) == 0x12U);
    CH_TEST_ASSERT(ip_stack_test_checksum(output->packet, 20U) == 0U);
    CH_TEST_ASSERT(ip_stack_test_tcp_checksum(
        output->packet, output->length) == 0U);
    uint32_t server_sequence = ip_stack_test_read_u32(output->packet + 24U);
    CH_TEST_ASSERT(ip_stack_test_read_u32(output->packet + 28U) == 101U);

    length = ip_stack_test_tcp_packet(
        packet, sizeof(packet), client_address, target_address,
        40000U, 443U, 101U, server_sequence + 1U, 0x10U, NULL, 0U);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(dial->count == 1U);
    CH_TEST_ASSERT_STRING("203.0.113.9:443", dial->target);
    CH_TEST_ASSERT_STRING("198.18.0.2:40000", dial->source);
    CH_TEST_ASSERT(dial->peer >= 0);

    static const uint8_t request[] = {'p', 'i', 'n', 'g'};
    length = ip_stack_test_tcp_packet(
        packet, sizeof(packet), client_address, target_address,
        40000U, 443U, 101U, server_sequence + 1U, 0x18U,
        request, sizeof(request));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    uint8_t received[16];
    CH_TEST_ASSERT(recv(dial->peer, received, sizeof(received), 0) ==
                   (ssize_t)sizeof(request));
    CH_TEST_ASSERT(memcmp(received, request, sizeof(request)) == 0);

    static const uint8_t response[] = {'p', 'o', 'n', 'g'};
    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(send(dial->peer, response, sizeof(response), 0) ==
                   (ssize_t)sizeof(response));
    ch_ip_stack_tick(stack);
    CH_TEST_ASSERT(output->count >= 1U);
    CH_TEST_ASSERT(output->length == 40U + sizeof(response));
    CH_TEST_ASSERT(memcmp(output->packet + 12U, target_address, 4U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 16U, client_address, 4U) == 0);
    CH_TEST_ASSERT(output->packet[20] == 0x01U &&
                   output->packet[21] == 0xbbU);
    CH_TEST_ASSERT(memcmp(output->packet + 40U, response,
                          sizeof(response)) == 0);
    CH_TEST_ASSERT(ip_stack_test_checksum(output->packet, 20U) == 0U);
    CH_TEST_ASSERT(ip_stack_test_tcp_checksum(
        output->packet, output->length) == 0U);
    CH_TEST_ASSERT(close(dial->peer) == 0);
    dial->peer = -1;
    ch_ip_stack_tick(stack);
}

void ch_test_ip_stack(void) {
    ip_stack_test_output output = {0};
    ip_stack_test_dial dial = {.peer = -1};
    ch_ip_stack_options options = {
        .packet_writer = ip_stack_test_writer,
        .packet_writer_context = &output,
        .tcp_dialer = ip_stack_test_tcp_dialer,
        .tcp_dialer_context = &dial
    };
    ch_error error;
    ch_ip_stack *stack = ch_ip_stack_create(&options, &error);
    CH_TEST_ASSERT(stack != NULL);
    CH_TEST_ASSERT(ch_ip_stack_create(&options, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_STATE);
    ip_stack_test_ipv4_echo(stack, &output);
    ip_stack_test_ipv6_echo(stack, &output);
    ip_stack_test_ipv4_tcp(stack, &output, &dial);
    const uint8_t invalid[] = {0x70U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U};
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, invalid, sizeof(invalid),
                                      &error) == CH_ERROR_PARSE);
    ch_ip_stack_tick(stack);
    ch_ip_stack_destroy(stack);
}
