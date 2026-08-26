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

typedef struct ip_stack_test_udp {
    char target[64];
    char source[64];
    char remote_source[64];
    char domain_hint[264];
    uint8_t sent[256];
    size_t sent_length;
    unsigned int dial_count;
    unsigned int send_count;
    unsigned int close_count;
    int response_pending;
} ip_stack_test_udp;

typedef struct ip_stack_test_dns {
    uint8_t query[256];
    size_t query_length;
    unsigned int count;
} ip_stack_test_dns;

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

static uint16_t ip_stack_test_tcp6_checksum(const uint8_t *packet,
                                            size_t length) {
    size_t tcp_length = length - 40U;
    uint32_t sum = ip_stack_test_sum(packet + 8U, 32U, 0U);
    uint8_t pseudo_tail[8] = {
        (uint8_t)(tcp_length >> 24U),
        (uint8_t)(tcp_length >> 16U),
        (uint8_t)(tcp_length >> 8U),
        (uint8_t)tcp_length,
        0U, 0U, 0U, 6U
    };
    sum = ip_stack_test_sum(pseudo_tail, sizeof(pseudo_tail), sum);
    sum = ip_stack_test_sum(packet + 40U, tcp_length, sum);
    return ip_stack_test_finish_sum(sum);
}

static uint16_t ip_stack_test_udp4_checksum(const uint8_t *packet,
                                            size_t length) {
    size_t udp_length = length - 20U;
    uint32_t sum = ip_stack_test_sum(packet + 12U, 8U, 0U);
    uint8_t pseudo[] = {
        0U, 17U, (uint8_t)(udp_length >> 8U), (uint8_t)udp_length
    };
    sum = ip_stack_test_sum(pseudo, sizeof(pseudo), sum);
    sum = ip_stack_test_sum(packet + 20U, udp_length, sum);
    return ip_stack_test_finish_sum(sum);
}

static uint16_t ip_stack_test_udp6_checksum(const uint8_t *packet,
                                            size_t length) {
    size_t udp_length = length - 40U;
    uint32_t sum = ip_stack_test_sum(packet + 8U, 32U, 0U);
    uint8_t pseudo_tail[8] = {
        (uint8_t)(udp_length >> 24U),
        (uint8_t)(udp_length >> 16U),
        (uint8_t)(udp_length >> 8U),
        (uint8_t)udp_length,
        0U, 0U, 0U, 17U
    };
    sum = ip_stack_test_sum(pseudo_tail, sizeof(pseudo_tail), sum);
    sum = ip_stack_test_sum(packet + 40U, udp_length, sum);
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

static size_t ip_stack_test_tcp6_packet(
    uint8_t *packet, size_t capacity, const uint8_t source[16],
    const uint8_t target[16], uint16_t source_port, uint16_t target_port,
    uint32_t sequence, uint32_t acknowledgement, uint8_t flags,
    const uint8_t *payload, size_t payload_length) {
    size_t tcp_length = 20U + payload_length;
    size_t length = 40U + tcp_length;
    if (capacity < length || tcp_length > UINT16_MAX) return 0U;
    memset(packet, 0, length);
    packet[0] = 0x60U;
    ip_stack_test_write_u16(packet + 4U, (uint16_t)tcp_length);
    packet[6] = 6U;
    packet[7] = 64U;
    memcpy(packet + 8U, source, 16U);
    memcpy(packet + 24U, target, 16U);
    ip_stack_test_write_u16(packet + 40U, source_port);
    ip_stack_test_write_u16(packet + 42U, target_port);
    ip_stack_test_write_u32(packet + 44U, sequence);
    ip_stack_test_write_u32(packet + 48U, acknowledgement);
    packet[52] = 0x50U;
    packet[53] = flags;
    packet[54] = 0xffU;
    packet[55] = 0xffU;
    if (payload_length > 0U) memcpy(packet + 60U, payload, payload_length);
    ip_stack_test_set_checksum(packet + 40U, 16U,
                               ip_stack_test_tcp6_checksum(packet, length));
    return length;
}

static size_t ip_stack_test_udp4_packet(
    uint8_t *packet, size_t capacity, const uint8_t source[4],
    const uint8_t target[4], uint16_t source_port, uint16_t target_port,
    const uint8_t *payload, size_t payload_length) {
    size_t length = 28U + payload_length;
    if (capacity < length || length > UINT16_MAX) return 0U;
    memset(packet, 0, length);
    packet[0] = 0x45U;
    ip_stack_test_write_u16(packet + 2U, (uint16_t)length);
    packet[4] = 0x12U;
    packet[5] = 0x34U;
    packet[6] = 0x40U;
    packet[8] = 64U;
    packet[9] = 17U;
    memcpy(packet + 12U, source, 4U);
    memcpy(packet + 16U, target, 4U);
    ip_stack_test_write_u16(packet + 20U, source_port);
    ip_stack_test_write_u16(packet + 22U, target_port);
    ip_stack_test_write_u16(packet + 24U,
                            (uint16_t)(8U + payload_length));
    if (payload_length > 0U) memcpy(packet + 28U, payload, payload_length);
    ip_stack_test_set_checksum(packet + 20U, 6U,
                               ip_stack_test_udp4_checksum(packet, length));
    ip_stack_test_set_checksum(packet, 10U,
                               ip_stack_test_checksum(packet, 20U));
    return length;
}

static size_t ip_stack_test_udp6_packet(
    uint8_t *packet, size_t capacity, const uint8_t source[16],
    const uint8_t target[16], uint16_t source_port, uint16_t target_port,
    const uint8_t *payload, size_t payload_length) {
    size_t udp_length = 8U + payload_length;
    size_t length = 40U + udp_length;
    if (capacity < length || udp_length > UINT16_MAX) return 0U;
    memset(packet, 0, length);
    packet[0] = 0x60U;
    ip_stack_test_write_u16(packet + 4U, (uint16_t)udp_length);
    packet[6] = 17U;
    packet[7] = 64U;
    memcpy(packet + 8U, source, 16U);
    memcpy(packet + 24U, target, 16U);
    ip_stack_test_write_u16(packet + 40U, source_port);
    ip_stack_test_write_u16(packet + 42U, target_port);
    ip_stack_test_write_u16(packet + 44U, (uint16_t)udp_length);
    if (payload_length > 0U) memcpy(packet + 48U, payload, payload_length);
    ip_stack_test_set_checksum(packet + 40U, 6U,
                               ip_stack_test_udp6_checksum(packet, length));
    return length;
}

static size_t ip_stack_test_ipv4_fragment(
    uint8_t *fragment, size_t capacity, const uint8_t *packet,
    size_t packet_length, size_t payload_offset, size_t payload_length,
    int more_fragments) {
    if (packet_length < 20U || payload_offset + payload_length >
            packet_length - 20U || capacity < 20U + payload_length ||
        (payload_offset & 7U) != 0U) {
        return 0U;
    }
    size_t length = 20U + payload_length;
    memcpy(fragment, packet, 20U);
    memcpy(fragment + 20U, packet + 20U + payload_offset, payload_length);
    ip_stack_test_write_u16(fragment + 2U, (uint16_t)length);
    uint16_t field = (uint16_t)(payload_offset / 8U);
    if (more_fragments) field |= 0x2000U;
    ip_stack_test_write_u16(fragment + 6U, field);
    fragment[10] = 0U;
    fragment[11] = 0U;
    ip_stack_test_set_checksum(fragment, 10U,
                               ip_stack_test_checksum(fragment, 20U));
    return length;
}

static size_t ip_stack_test_ipv6_fragment(
    uint8_t *fragment, size_t capacity, const uint8_t *packet,
    size_t packet_length, size_t payload_offset, size_t payload_length,
    int more_fragments, uint32_t identifier) {
    if (packet_length < 40U || payload_offset + payload_length >
            packet_length - 40U || capacity < 48U + payload_length ||
        (payload_offset & 7U) != 0U) {
        return 0U;
    }
    size_t length = 48U + payload_length;
    memcpy(fragment, packet, 40U);
    fragment[6] = 44U;
    ip_stack_test_write_u16(fragment + 4U,
                            (uint16_t)(8U + payload_length));
    fragment[40] = packet[6];
    fragment[41] = 0U;
    uint16_t field = (uint16_t)payload_offset;
    if (more_fragments) field |= 1U;
    ip_stack_test_write_u16(fragment + 42U, field);
    ip_stack_test_write_u32(fragment + 44U, identifier);
    memcpy(fragment + 48U, packet + 40U + payload_offset, payload_length);
    return length;
}

static ch_status ip_stack_test_tcp_dialer(
    const char *target, const char *source, const char *domain_hint,
    int *out_descriptor, void *context, ch_error *error) {
    (void)domain_hint;
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

static ch_status ip_stack_test_udp_dialer(
    const char *target, const char *source, const char *domain_hint,
    void **out_connection, void *context, ch_error *error) {
    ip_stack_test_udp *udp = context;
    ch_error_clear(error);
    ++udp->dial_count;
    (void)snprintf(udp->target, sizeof(udp->target), "%s", target);
    (void)snprintf(udp->source, sizeof(udp->source), "%s", source);
    (void)snprintf(udp->remote_source, sizeof(udp->remote_source), "%s",
                   target);
    (void)snprintf(udp->domain_hint, sizeof(udp->domain_hint), "%s",
                   domain_hint);
    *out_connection = udp;
    return CH_OK;
}

static ch_status ip_stack_test_udp_send(
    void *connection, const char *target, const uint8_t *payload,
    size_t payload_length, ch_error *error) {
    ip_stack_test_udp *udp = connection;
    ch_error_clear(error);
    if (payload_length > sizeof(udp->sent)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "test UDP payload too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ++udp->send_count;
    (void)snprintf(udp->target, sizeof(udp->target), "%s", target);
    memcpy(udp->sent, payload, payload_length);
    udp->sent_length = payload_length;
    udp->response_pending = 1;
    return CH_OK;
}

static ch_status ip_stack_test_udp_receive(
    void *connection, uint8_t *buffer, size_t buffer_capacity,
    size_t *out_length, char **out_source, ch_error *error) {
    ip_stack_test_udp *udp = connection;
    static const uint8_t response[] = {'u', 'd', 'p', '-', 'o', 'k'};
    ch_error_clear(error);
    *out_length = 0U;
    *out_source = NULL;
    if (!udp->response_pending) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "no test UDP response");
        return CH_ERROR_NOT_FOUND;
    }
    if (buffer_capacity < sizeof(response)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "test UDP receive buffer too small");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_source = strdup(udp->remote_source);
    if (*out_source == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy test UDP source");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(buffer, response, sizeof(response));
    *out_length = sizeof(response);
    udp->response_pending = 0;
    return CH_OK;
}

static void ip_stack_test_udp_close(void *connection) {
    ip_stack_test_udp *udp = connection;
    ++udp->close_count;
}

static ch_status ip_stack_test_dns_exchange(
    const uint8_t *query, size_t query_length, uint8_t **out_response,
    size_t *out_response_length, void *context, ch_error *error) {
    ip_stack_test_dns *dns = context;
    ch_error_clear(error);
    *out_response = NULL;
    *out_response_length = 0U;
    if (query_length > sizeof(dns->query)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "test DNS query too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ++dns->count;
    memcpy(dns->query, query, query_length);
    dns->query_length = query_length;
    size_t response_length = query_length + 16U;
    uint8_t *response = calloc(1U, response_length);
    if (response == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate test DNS response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(response, query, query_length);
    response[2] |= 0x80U;
    response[6] = 0U;
    response[7] = 1U;
    uint8_t answer[16] = {
        0xc0U, 0x0cU, 0U, 1U, 0U, 1U, 0U, 0U,
        0U, 60U, 0U, 4U, 203U, 0U, 113U, 77U
    };
    memcpy(response + query_length, answer, sizeof(answer));
    *out_response = response;
    *out_response_length = response_length;
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

static void ip_stack_test_ipv6_tcp(ch_ip_stack *stack,
                                   ip_stack_test_output *output,
                                   ip_stack_test_dial *dial) {
    static const uint8_t client_address[16] = {
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 2U
    };
    static const uint8_t target_address[16] = {
        0x20U, 0x01U, 0x0dU, 0xb8U, 0U, 0U, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 9U
    };
    uint8_t packet[256];
    size_t length = ip_stack_test_tcp6_packet(
        packet, sizeof(packet), client_address, target_address,
        41000U, 8443U, 200U, 0U, 0x02U, NULL, 0U);
    CH_TEST_ASSERT(length == 60U);
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length >= 60U);
    CH_TEST_ASSERT(memcmp(output->packet + 8U, target_address, 16U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 24U, client_address, 16U) == 0);
    CH_TEST_ASSERT(output->packet[40] == 0x20U &&
                   output->packet[41] == 0xfbU);
    CH_TEST_ASSERT(output->packet[42] == 0xa0U &&
                   output->packet[43] == 0x28U);
    CH_TEST_ASSERT((output->packet[53] & 0x12U) == 0x12U);
    CH_TEST_ASSERT(ip_stack_test_tcp6_checksum(
        output->packet, output->length) == 0U);
    uint32_t server_sequence = ip_stack_test_read_u32(output->packet + 44U);
    CH_TEST_ASSERT(ip_stack_test_read_u32(output->packet + 48U) == 201U);

    length = ip_stack_test_tcp6_packet(
        packet, sizeof(packet), client_address, target_address,
        41000U, 8443U, 201U, server_sequence + 1U, 0x10U, NULL, 0U);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(dial->count == 2U);
    CH_TEST_ASSERT_STRING("[2001:db8::9]:8443", dial->target);
    CH_TEST_ASSERT_STRING("[fd7a:636c:616d::2]:41000", dial->source);
    CH_TEST_ASSERT(dial->peer >= 0);

    static const uint8_t request[] = {'v', '6', '-', 'p', 'i', 'n', 'g'};
    length = ip_stack_test_tcp6_packet(
        packet, sizeof(packet), client_address, target_address,
        41000U, 8443U, 201U, server_sequence + 1U, 0x18U,
        request, sizeof(request));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    uint8_t received[16];
    CH_TEST_ASSERT(recv(dial->peer, received, sizeof(received), 0) ==
                   (ssize_t)sizeof(request));
    CH_TEST_ASSERT(memcmp(received, request, sizeof(request)) == 0);

    static const uint8_t response[] = {'v', '6', '-', 'p', 'o', 'n', 'g'};
    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(send(dial->peer, response, sizeof(response), 0) ==
                   (ssize_t)sizeof(response));
    ch_ip_stack_tick(stack);
    CH_TEST_ASSERT(output->count >= 1U);
    CH_TEST_ASSERT(output->length == 60U + sizeof(response));
    CH_TEST_ASSERT(memcmp(output->packet + 8U, target_address, 16U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 24U, client_address, 16U) == 0);
    CH_TEST_ASSERT(output->packet[40] == 0x20U &&
                   output->packet[41] == 0xfbU);
    CH_TEST_ASSERT(memcmp(output->packet + 60U, response,
                          sizeof(response)) == 0);
    CH_TEST_ASSERT(ip_stack_test_tcp6_checksum(
        output->packet, output->length) == 0U);
    CH_TEST_ASSERT(close(dial->peer) == 0);
    dial->peer = -1;
    ch_ip_stack_tick(stack);
}

static void ip_stack_test_ipv4_udp(ch_ip_stack *stack,
                                   ip_stack_test_output *output,
                                   ip_stack_test_udp *udp) {
    static const uint8_t client_address[] = {198U, 18U, 0U, 2U};
    static const uint8_t target_address[] = {203U, 0U, 113U, 53U};
    static const uint8_t request[] = {'u', 'd', 'p', '-', 'v', '4'};
    uint8_t packet[256];
    size_t length = ip_stack_test_udp4_packet(
        packet, sizeof(packet), client_address, target_address,
        42000U, 5353U, request, sizeof(request));
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 1U);
    CH_TEST_ASSERT(udp->send_count == 1U);
    CH_TEST_ASSERT_STRING("203.0.113.53:5353", udp->target);
    CH_TEST_ASSERT_STRING("198.18.0.2:42000", udp->source);
    CH_TEST_ASSERT(udp->sent_length == sizeof(request));
    CH_TEST_ASSERT(memcmp(udp->sent, request, sizeof(request)) == 0);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length == 34U);
    CH_TEST_ASSERT(memcmp(output->packet + 12U, target_address, 4U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 16U, client_address, 4U) == 0);
    CH_TEST_ASSERT(output->packet[20] == 0x14U &&
                   output->packet[21] == 0xe9U);
    CH_TEST_ASSERT(output->packet[22] == 0xa4U &&
                   output->packet[23] == 0x10U);
    CH_TEST_ASSERT(ip_stack_test_checksum(output->packet, 20U) == 0U);
    CH_TEST_ASSERT(ip_stack_test_udp4_checksum(
        output->packet, output->length) == 0U);
    CH_TEST_ASSERT(memcmp(output->packet + 28U, "udp-ok", 6U) == 0);

    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 1U);
    CH_TEST_ASSERT(udp->send_count == 2U);
    CH_TEST_ASSERT(output->count == 1U);
}

static void ip_stack_test_ipv6_udp(ch_ip_stack *stack,
                                   ip_stack_test_output *output,
                                   ip_stack_test_udp *udp) {
    static const uint8_t client_address[16] = {
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 2U
    };
    static const uint8_t target_address[16] = {
        0x20U, 0x01U, 0x0dU, 0xb8U, 0U, 0U, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 0x35U
    };
    static const uint8_t request[] = {'u', 'd', 'p', '-', 'v', '6'};
    uint8_t packet[256];
    size_t length = ip_stack_test_udp6_packet(
        packet, sizeof(packet), client_address, target_address,
        43000U, 5353U, request, sizeof(request));
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 2U);
    CH_TEST_ASSERT(udp->send_count == 3U);
    CH_TEST_ASSERT_STRING("[2001:db8::35]:5353", udp->target);
    CH_TEST_ASSERT_STRING("[fd7a:636c:616d::2]:43000", udp->source);
    CH_TEST_ASSERT(udp->sent_length == sizeof(request));
    CH_TEST_ASSERT(memcmp(udp->sent, request, sizeof(request)) == 0);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length == 54U);
    CH_TEST_ASSERT(memcmp(output->packet + 8U, target_address, 16U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 24U, client_address, 16U) == 0);
    CH_TEST_ASSERT(output->packet[40] == 0x14U &&
                   output->packet[41] == 0xe9U);
    CH_TEST_ASSERT(output->packet[42] == 0xa7U &&
                   output->packet[43] == 0xf8U);
    CH_TEST_ASSERT(ip_stack_test_udp6_checksum(
        output->packet, output->length) == 0U);
    CH_TEST_ASSERT(memcmp(output->packet + 48U, "udp-ok", 6U) == 0);
}

static void ip_stack_test_dns_interception(ch_ip_stack *stack,
                                           ip_stack_test_output *output,
                                           ip_stack_test_udp *udp,
                                           ip_stack_test_dns *dns) {
    static const uint8_t client_address[] = {198U, 18U, 0U, 2U};
    static const uint8_t target_address[] = {1U, 1U, 1U, 1U};
    static const uint8_t query[29] = {
        0x12U, 0x34U, 0x01U, 0x00U, 0x00U, 0x01U,
        0x00U, 0x00U, 0x00U, 0x00U, 0x00U, 0x00U,
        7U, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
        3U, 'c', 'o', 'm', 0U, 0U, 1U, 0U, 1U
    };
    uint8_t packet[128];
    size_t length = ip_stack_test_udp4_packet(
        packet, sizeof(packet), client_address, target_address,
        44000U, 53U, query, sizeof(query));
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(dns->count == 1U);
    CH_TEST_ASSERT(dns->query_length == sizeof(query));
    CH_TEST_ASSERT(memcmp(dns->query, query, sizeof(query)) == 0);
    CH_TEST_ASSERT(udp->dial_count == 2U);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(output->length == 73U);
    CH_TEST_ASSERT(memcmp(output->packet + 12U, target_address, 4U) == 0);
    CH_TEST_ASSERT(memcmp(output->packet + 16U, client_address, 4U) == 0);
    CH_TEST_ASSERT(output->packet[20] == 0U && output->packet[21] == 53U);
    CH_TEST_ASSERT(output->packet[22] == 0xabU &&
                   output->packet[23] == 0xe0U);
    CH_TEST_ASSERT((output->packet[30] & 0x80U) != 0U);
    CH_TEST_ASSERT(ip_stack_test_udp4_checksum(
        output->packet, output->length) == 0U);

    static const uint8_t recovered_target[] = {203U, 0U, 113U, 77U};
    static const uint8_t payload[] = {'d', 'o', 'm', 'a', 'i', 'n'};
    length = ip_stack_test_udp4_packet(
        packet, sizeof(packet), client_address, recovered_target,
        45000U, 9999U, payload, sizeof(payload));
    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 3U);
    CH_TEST_ASSERT_STRING("203.0.113.77:9999", udp->target);
    CH_TEST_ASSERT_STRING("example.com:9999", udp->domain_hint);
}

static void ip_stack_test_fragmented_udp(ch_ip_stack *stack,
                                         ip_stack_test_output *output,
                                         ip_stack_test_udp *udp) {
    static const uint8_t client4[] = {198U, 18U, 0U, 2U};
    static const uint8_t target4[] = {192U, 0U, 2U, 44U};
    static const uint8_t payload[] = {'f', 'r', 'a', 'g', '-', 'o', 'k'};
    uint8_t packet[256];
    size_t packet_length = ip_stack_test_udp4_packet(
        packet, sizeof(packet), client4, target4, 46000U, 9000U,
        payload, sizeof(payload));
    uint8_t first[128];
    uint8_t second[128];
    size_t first_length = ip_stack_test_ipv4_fragment(
        first, sizeof(first), packet, packet_length, 0U, 8U, 1);
    size_t second_length = ip_stack_test_ipv4_fragment(
        second, sizeof(second), packet, packet_length, 8U,
        packet_length - 28U, 0);
    ch_error error;
    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, second, second_length,
                                      &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 0U);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, first, first_length,
                                      &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 4U);
    CH_TEST_ASSERT_STRING("192.0.2.44:9000", udp->target);
    CH_TEST_ASSERT(output->count == 1U);

    static const uint8_t client6[16] = {
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 2U
    };
    static const uint8_t target6[16] = {
        0x20U, 0x01U, 0x0dU, 0xb8U, 0U, 0U, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 0x44U
    };
    packet_length = ip_stack_test_udp6_packet(
        packet, sizeof(packet), client6, target6, 47000U, 9001U,
        payload, sizeof(payload));
    first_length = ip_stack_test_ipv6_fragment(
        first, sizeof(first), packet, packet_length, 0U, 8U, 1,
        UINT32_C(0x10203040));
    second_length = ip_stack_test_ipv6_fragment(
        second, sizeof(second), packet, packet_length, 8U,
        packet_length - 48U, 0, UINT32_C(0x10203040));
    memset(output, 0, sizeof(*output));
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, first, first_length,
                                      &error) == CH_OK);
    CH_TEST_ASSERT(output->count == 0U);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, second, second_length,
                                      &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 5U);
    CH_TEST_ASSERT_STRING("[2001:db8::44]:9001", udp->target);
    CH_TEST_ASSERT(output->count == 1U);

    packet_length = ip_stack_test_udp4_packet(
        packet, sizeof(packet), client4, target4, 48000U, 9002U,
        payload, sizeof(payload));
    first_length = ip_stack_test_ipv4_fragment(
        first, sizeof(first), packet, packet_length, 0U, 8U, 1);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, first, first_length,
                                      &error) == CH_OK);
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, first, first_length,
                                      &error) == CH_ERROR_PARSE);
    CH_TEST_ASSERT(strstr(error.message, "overlapping") != NULL);
}

static void ip_stack_test_ipv6_extension_udp(ch_ip_stack *stack,
                                             ip_stack_test_output *output,
                                             ip_stack_test_udp *udp) {
    static const uint8_t client[16] = {
        0xfdU, 0x7aU, 0x63U, 0x6cU, 0x61U, 0x6dU, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 2U
    };
    static const uint8_t target[16] = {
        0x20U, 0x01U, 0x0dU, 0xb8U, 0U, 0U, 0U, 0U,
        0U, 0U, 0U, 0U, 0U, 0U, 0U, 0x60U
    };
    static const uint8_t payload[] = {'e', 'x', 't'};
    uint8_t plain[128];
    size_t plain_length = ip_stack_test_udp6_packet(
        plain, sizeof(plain), client, target, 49000U, 9003U,
        payload, sizeof(payload));
    uint8_t packet[136];
    memcpy(packet, plain, 40U);
    packet[6] = 60U;
    ip_stack_test_write_u16(packet + 4U,
                            (uint16_t)(plain_length - 40U + 8U));
    memset(packet + 40U, 0, 8U);
    packet[40] = 17U;
    memcpy(packet + 48U, plain + 40U, plain_length - 40U);
    size_t length = plain_length + 8U;
    memset(output, 0, sizeof(*output));
    ch_error error;
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, packet, length, &error) == CH_OK);
    CH_TEST_ASSERT(udp->dial_count == 6U);
    CH_TEST_ASSERT_STRING("[2001:db8::60]:9003", udp->target);
    CH_TEST_ASSERT(output->count == 1U);
    CH_TEST_ASSERT(ip_stack_test_udp6_checksum(
        output->packet, output->length) == 0U);
}

void ch_test_ip_stack(void) {
    ip_stack_test_output output = {0};
    ip_stack_test_dial dial = {.peer = -1};
    ip_stack_test_udp udp = {0};
    ip_stack_test_dns dns = {0};
    ch_ip_stack_options options = {
        .packet_writer = ip_stack_test_writer,
        .packet_writer_context = &output,
        .tcp_dialer = ip_stack_test_tcp_dialer,
        .tcp_dialer_context = &dial,
        .udp_dialer = ip_stack_test_udp_dialer,
        .udp_dialer_context = &udp,
        .udp_sender = ip_stack_test_udp_send,
        .udp_receiver = ip_stack_test_udp_receive,
        .udp_closer = ip_stack_test_udp_close,
        .dns_exchange = ip_stack_test_dns_exchange,
        .dns_exchange_context = &dns
    };
    ch_error error;
    ch_ip_stack *stack = ch_ip_stack_create(&options, &error);
    CH_TEST_ASSERT(stack != NULL);
    CH_TEST_ASSERT(ch_ip_stack_create(&options, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_STATE);
    ip_stack_test_ipv4_echo(stack, &output);
    ip_stack_test_ipv6_echo(stack, &output);
    ip_stack_test_ipv4_tcp(stack, &output, &dial);
    ip_stack_test_ipv6_tcp(stack, &output, &dial);
    ip_stack_test_ipv4_udp(stack, &output, &udp);
    ip_stack_test_ipv6_udp(stack, &output, &udp);
    ip_stack_test_dns_interception(stack, &output, &udp, &dns);
    ip_stack_test_fragmented_udp(stack, &output, &udp);
    ip_stack_test_ipv6_extension_udp(stack, &output, &udp);
    const uint8_t invalid[] = {0x70U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U, 0U, 0U, 0U, 0U,
                               0U, 0U, 0U, 0U};
    CH_TEST_ASSERT(ch_ip_stack_inject(stack, invalid, sizeof(invalid),
                                      &error) == CH_ERROR_PARSE);
    ch_ip_stack_tick(stack);
    ch_ip_stack_destroy(stack);
    CH_TEST_ASSERT(udp.close_count == 6U);
}
