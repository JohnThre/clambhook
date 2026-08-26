#include "test.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/ip_stack.h"

typedef struct ip_stack_test_output {
    uint8_t packet[2048];
    size_t length;
    unsigned int count;
} ip_stack_test_output;

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

void ch_test_ip_stack(void) {
    ip_stack_test_output output = {0};
    ch_ip_stack_options options = {
        .packet_writer = ip_stack_test_writer,
        .packet_writer_context = &output
    };
    ch_error error;
    ch_ip_stack *stack = ch_ip_stack_create(&options, &error);
    CH_TEST_ASSERT(stack != NULL);
    CH_TEST_ASSERT(ch_ip_stack_create(&options, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_STATE);
    ip_stack_test_ipv4_echo(stack, &output);
    ip_stack_test_ipv6_echo(stack, &output);
    const uint8_t invalid[] = {0x70U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U};
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, invalid, sizeof(invalid),
                                      &error) == CH_ERROR_PARSE);
    ch_ip_stack_tick(stack);
    ch_ip_stack_destroy(stack);
}
