#include "clambhook/ip_stack.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/random.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "lwip/init.h"
#include "lwip/ip.h"
#include "lwip/ip4_addr.h"
#include "lwip/ip6_addr.h"
#include "lwip/netif.h"
#include "lwip/pbuf.h"
#include "lwip/priv/tcp_priv.h"
#include "lwip/tcp.h"
#include "lwip/timeouts.h"

#include "internal.h"
#include "lwip_context.h"

#define CH_IP_STACK_DEFAULT_MTU 1500U
#define CH_IP_STACK_MAX_PACKET 65535U
#define CH_IP_STACK_TCP_FIRST_PORT 20000U
#define CH_IP_STACK_TCP_LAST_PORT 59999U
#define CH_IP_STACK_TCP_QUEUE_LIMIT (1024U * 1024U)
#define CH_IP_STACK_TCP_READ_SIZE 16384U
#define CH_IP_STACK_TCP_FLOW_LIMIT 512U
#define CH_IP_STACK_TCP_HANDSHAKE_TIMEOUT_MS 30000U
#define CH_IP_STACK_UDP_FLOW_LIMIT 512U
#define CH_IP_STACK_UDP_FLOW_TIMEOUT_MS 120000U
#define CH_IP_STACK_DNS_CACHE_LIMIT 1024U
#define CH_IP_STACK_DNS_MAX_TTL_MS 86400000U
#define CH_IP_STACK_DNS_NAME_CAPACITY 256U
#define CH_IP_STACK_DOMAIN_HINT_CAPACITY 264U
#define CH_IP_STACK_FRAGMENT_FLOW_LIMIT 64U
#define CH_IP_STACK_FRAGMENT_TIMEOUT_MS 30000U
#define CH_IP_STACK_FRAGMENT_DATA_CAPACITY 65535U
#define CH_IP_STACK_FRAGMENT_COVERAGE_BYTES 8192U
#define CH_IP_STACK_EXTENSION_HEADER_LIMIT 512U

typedef struct ch_ip_stack_tcp_flow ch_ip_stack_tcp_flow;
typedef struct ch_ip_stack_udp_flow ch_ip_stack_udp_flow;
typedef struct ch_ip_stack_dns_entry ch_ip_stack_dns_entry;
typedef struct ch_ip_stack_fragment_flow ch_ip_stack_fragment_flow;

struct ch_ip_stack_tcp_flow {
    ch_ip_stack_tcp_flow *next;
    struct ch_ip_stack *stack;
    struct tcp_pcb *listener;
    struct tcp_pcb *client;
    int descriptor;
    uint8_t source_address[16];
    uint8_t target_address[16];
    int address_family;
    uint16_t source_port;
    uint16_t target_port;
    uint16_t internal_port;
    u32_t created_at;
    char domain_hint[CH_IP_STACK_DOMAIN_HINT_CAPACITY];
    uint8_t *pending;
    size_t pending_offset;
    size_t pending_length;
    uint8_t *remote_pending;
    size_t remote_pending_length;
    int client_eof;
    int remote_eof;
    int dead;
    uint64_t flow_id;
    int close_observed;
};

struct ch_ip_stack_udp_flow {
    ch_ip_stack_udp_flow *next;
    struct ch_ip_stack *stack;
    void *connection;
    uint8_t source_address[16];
    uint8_t target_address[16];
    int address_family;
    uint16_t source_port;
    uint16_t target_port;
    char domain_hint[CH_IP_STACK_DOMAIN_HINT_CAPACITY];
    u32_t last_activity;
    int dead;
    uint64_t flow_id;
    int close_observed;
};

struct ch_ip_stack_dns_entry {
    uint8_t address[16];
    int address_family;
    char domain[CH_IP_STACK_DNS_NAME_CAPACITY];
    u32_t expires_at;
    uint64_t sequence;
};

struct ch_ip_stack_fragment_flow {
    ch_ip_stack_fragment_flow *next;
    uint8_t source_address[16];
    uint8_t target_address[16];
    uint8_t header[CH_IP_STACK_EXTENSION_HEADER_LIMIT];
    uint8_t *data;
    uint8_t *coverage;
    size_t header_length;
    size_t ipv6_next_header_offset;
    size_t total_payload_length;
    size_t covered_length;
    uint32_t identifier;
    int address_family;
    uint8_t protocol;
    int total_known;
    int have_first;
    int dead;
    u32_t last_activity;
};

struct ch_ip_stack {
    struct netif interface;
    struct netif *previous_default;
    ch_ip_stack_packet_writer packet_writer;
    void *packet_writer_context;
    ch_ip_stack_tcp_dialer tcp_dialer;
    void *tcp_dialer_context;
    ch_ip_stack_udp_dialer udp_dialer;
    void *udp_dialer_context;
    ch_ip_stack_udp_sender udp_sender;
    ch_ip_stack_udp_receiver udp_receiver;
    ch_ip_stack_udp_closer udp_closer;
    ch_ip_stack_flow_bytes_observer flow_bytes;
    ch_ip_stack_flow_close_observer flow_close;
    void *flow_observer_context;
    ch_ip_stack_dns_exchange dns_exchange;
    void *dns_exchange_context;
    ch_ip_stack_tcp_flow *tcp_flows;
    size_t tcp_flow_count;
    ch_ip_stack_udp_flow *udp_flows;
    size_t udp_flow_count;
    ch_ip_stack_dns_entry *dns_entries;
    size_t dns_entry_count;
    uint64_t dns_sequence;
    ch_ip_stack_fragment_flow *fragment_flows;
    size_t fragment_flow_count;
    uint16_t next_tcp_port;
    s8_t ipv6_index;
    unsigned int mtu;
};

static atomic_uint_fast32_t ch_ip_stack_random_fallback =
    UINT32_C(0x9e3779b9);

u32_t sys_now(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) return 0U;
    uint64_t milliseconds = (uint64_t)now.tv_sec * UINT64_C(1000) +
        (uint64_t)now.tv_nsec / UINT64_C(1000000);
    return (u32_t)milliseconds;
}

uint32_t ch_lwip_random(void) {
    uint32_t value = 0U;
#if defined(__APPLE__)
    if (getentropy(&value, sizeof(value)) == 0) return value;
#else
    if (getrandom(&value, sizeof(value), 0U) == (ssize_t)sizeof(value)) {
        return value;
    }
#endif
    struct timespec now;
    (void)clock_gettime(CLOCK_MONOTONIC, &now);
    uint32_t sequence = (uint32_t)atomic_fetch_add_explicit(
        &ch_ip_stack_random_fallback, UINT32_C(0x9e3779b9),
        memory_order_relaxed);
    value = sequence ^ (uint32_t)now.tv_nsec ^ (uint32_t)now.tv_sec;
    value ^= value >> 16U;
    value *= UINT32_C(0x7feb352d);
    value ^= value >> 15U;
    value *= UINT32_C(0x846ca68b);
    return value ^ (value >> 16U);
}

static uint16_t ch_ip_stack_read_u16(const uint8_t *bytes) {
    return (uint16_t)(((uint16_t)bytes[0] << 8U) | bytes[1]);
}

static uint32_t ch_ip_stack_read_u32(const uint8_t *bytes) {
    return ((uint32_t)bytes[0] << 24U) | ((uint32_t)bytes[1] << 16U) |
        ((uint32_t)bytes[2] << 8U) | bytes[3];
}

static void ch_ip_stack_write_u16(uint8_t *bytes, uint16_t value) {
    bytes[0] = (uint8_t)(value >> 8U);
    bytes[1] = (uint8_t)value;
}

static uint32_t ch_ip_stack_checksum_add(const uint8_t *bytes, size_t length,
                                         uint32_t sum) {
    while (length >= 2U) {
        sum += ((uint32_t)bytes[0] << 8U) | bytes[1];
        bytes += 2U;
        length -= 2U;
    }
    if (length > 0U) sum += (uint32_t)bytes[0] << 8U;
    return sum;
}

static uint16_t ch_ip_stack_checksum_finish(uint32_t sum) {
    while ((sum >> 16U) != 0U) sum = (sum & 0xffffU) + (sum >> 16U);
    return (uint16_t)~sum;
}

static uint16_t ch_ip_stack_checksum(const uint8_t *bytes, size_t length) {
    return ch_ip_stack_checksum_finish(
        ch_ip_stack_checksum_add(bytes, length, 0U));
}

static void ch_ip_stack_ipv4_checksums(uint8_t *packet, size_t length) {
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    if (header_length < 20U || header_length > length) return;
    packet[10] = 0U;
    packet[11] = 0U;
    ch_ip_stack_write_u16(packet + 10U,
                          ch_ip_stack_checksum(packet, header_length));
    if (packet[9] != 6U || length < header_length + 20U) return;
    size_t tcp_length = length - header_length;
    packet[header_length + 16U] = 0U;
    packet[header_length + 17U] = 0U;
    uint32_t sum = ch_ip_stack_checksum_add(packet + 12U, 8U, 0U);
    uint8_t pseudo[4] = {
        0U, 6U, (uint8_t)(tcp_length >> 8U), (uint8_t)tcp_length
    };
    sum = ch_ip_stack_checksum_add(pseudo, sizeof(pseudo), sum);
    sum = ch_ip_stack_checksum_add(packet + header_length, tcp_length, sum);
    ch_ip_stack_write_u16(packet + header_length + 16U,
                          ch_ip_stack_checksum_finish(sum));
}

static int ch_ip_stack_ipv6_transport(
    const uint8_t *packet, size_t length, uint8_t *out_protocol,
    size_t *out_offset) {
    if (length < 40U || (packet[0] >> 4U) != 6U ||
        ch_ip_stack_read_u16(packet + 4U) != length - 40U) {
        return 0;
    }
    uint8_t protocol = packet[6];
    size_t offset = 40U;
    unsigned int extension_count = 0U;
    while (protocol == 0U || protocol == 43U || protocol == 60U ||
           protocol == 51U) {
        if (++extension_count > 16U || offset + 2U > length) return 0;
        size_t extension_length = protocol == 51U ?
            ((size_t)packet[offset + 1U] + 2U) * 4U :
            ((size_t)packet[offset + 1U] + 1U) * 8U;
        if (extension_length < 8U || offset + extension_length > length ||
            offset + extension_length > CH_IP_STACK_EXTENSION_HEADER_LIMIT) {
            return 0;
        }
        protocol = packet[offset];
        offset += extension_length;
    }
    if (protocol == 44U) return 0;
    *out_protocol = protocol;
    *out_offset = offset;
    return 1;
}

static void ch_ip_stack_ipv6_tcp_checksum(uint8_t *packet, size_t length) {
    uint8_t protocol = 0U;
    size_t tcp_offset = 0U;
    if (!ch_ip_stack_ipv6_transport(packet, length, &protocol, &tcp_offset) ||
        protocol != 6U || length < tcp_offset + 20U) {
        return;
    }
    size_t tcp_length = length - tcp_offset;
    packet[tcp_offset + 16U] = 0U;
    packet[tcp_offset + 17U] = 0U;
    uint32_t sum = ch_ip_stack_checksum_add(packet + 8U, 32U, 0U);
    uint8_t pseudo[8] = {
        (uint8_t)(tcp_length >> 24U),
        (uint8_t)(tcp_length >> 16U),
        (uint8_t)(tcp_length >> 8U),
        (uint8_t)tcp_length,
        0U, 0U, 0U, 6U
    };
    sum = ch_ip_stack_checksum_add(pseudo, sizeof(pseudo), sum);
    sum = ch_ip_stack_checksum_add(packet + tcp_offset, tcp_length, sum);
    ch_ip_stack_write_u16(packet + tcp_offset + 16U,
                          ch_ip_stack_checksum_finish(sum));
}

static size_t ch_ip_stack_address_length(int address_family) {
    return address_family == AF_INET6 ? 16U : 4U;
}

static int ch_ip_stack_format_endpoint(
    int address_family, const uint8_t *address, uint16_t port,
    char endpoint[INET6_ADDRSTRLEN + 10U]) {
    char host[INET6_ADDRSTRLEN];
    if (inet_ntop(address_family, address, host, sizeof(host)) == NULL) {
        return 0;
    }
    int length = snprintf(endpoint, INET6_ADDRSTRLEN + 10U,
                          address_family == AF_INET6 ? "[%s]:%u" : "%s:%u",
                          host, (unsigned int)port);
    return length > 0 && (size_t)length < INET6_ADDRSTRLEN + 10U;
}

static int ch_ip_stack_parse_endpoint(const char *endpoint,
                                      int address_family,
                                      uint8_t address[16],
                                      uint16_t *out_port) {
    if (endpoint == NULL || out_port == NULL) return 0;
    const char *host_start = endpoint;
    const char *host_end = NULL;
    const char *port_start = NULL;
    if (address_family == AF_INET6) {
        if (endpoint[0] != '[') return 0;
        host_start = endpoint + 1U;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            return 0;
        }
        port_start = host_end + 2U;
    } else {
        host_end = strrchr(endpoint, ':');
        if (host_end == NULL || host_end == endpoint || host_end[1] == '\0') {
            return 0;
        }
        port_start = host_end + 1U;
    }
    size_t host_length = (size_t)(host_end - host_start);
    if (host_length == 0U || host_length >= INET6_ADDRSTRLEN) return 0;
    char host[INET6_ADDRSTRLEN];
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    char *port_end = NULL;
    errno = 0;
    unsigned long port = strtoul(port_start, &port_end, 10);
    if (errno != 0 || port_end == port_start || *port_end != '\0' ||
        port == 0UL || port > UINT16_MAX ||
        inet_pton(address_family, host, address) != 1) {
        return 0;
    }
    *out_port = (uint16_t)port;
    return 1;
}

static int ch_ip_stack_dns_read_name(
    const uint8_t *message, size_t length, size_t offset,
    char name[CH_IP_STACK_DNS_NAME_CAPACITY], size_t *out_next) {
    size_t cursor = offset;
    size_t next = offset;
    size_t written = 0U;
    unsigned int steps = 0U;
    int jumped = 0;
    while (cursor < length && steps++ < 128U) {
        uint8_t label_length = message[cursor];
        if ((label_length & 0xc0U) == 0xc0U) {
            if (cursor + 1U >= length) return 0;
            size_t pointer = ((size_t)(label_length & 0x3fU) << 8U) |
                message[cursor + 1U];
            if (pointer >= length) return 0;
            if (!jumped) next = cursor + 2U;
            cursor = pointer;
            jumped = 1;
            continue;
        }
        if ((label_length & 0xc0U) != 0U || label_length > 63U) return 0;
        ++cursor;
        if (label_length == 0U) {
            if (!jumped) next = cursor;
            if (written == 0U) return 0;
            name[written] = '\0';
            *out_next = next;
            return 1;
        }
        if (cursor + label_length > length ||
            written + (written == 0U ? 0U : 1U) + label_length >=
                CH_IP_STACK_DNS_NAME_CAPACITY) {
            return 0;
        }
        if (written > 0U) name[written++] = '.';
        for (size_t index = 0U; index < label_length; ++index) {
            unsigned char character = message[cursor + index];
            name[written++] = (char)tolower(character);
        }
        cursor += label_length;
        if (!jumped) next = cursor;
    }
    return 0;
}

static int ch_ip_stack_dns_entry_alive(const ch_ip_stack_dns_entry *entry,
                                       u32_t now) {
    return (int32_t)(entry->expires_at - now) > 0;
}

static void ch_ip_stack_dns_cache_store(
    ch_ip_stack *stack, int address_family, const uint8_t *address,
    const char *domain, uint32_t ttl_seconds) {
    u32_t now = sys_now();
    uint64_t ttl_milliseconds = (uint64_t)ttl_seconds * UINT64_C(1000);
    if (ttl_milliseconds == 0U) ttl_milliseconds = 1000U;
    if (ttl_milliseconds > CH_IP_STACK_DNS_MAX_TTL_MS) {
        ttl_milliseconds = CH_IP_STACK_DNS_MAX_TTL_MS;
    }
    size_t address_length = ch_ip_stack_address_length(address_family);
    size_t selected = stack->dns_entry_count;
    uint64_t oldest_sequence = UINT64_MAX;
    for (size_t index = 0U; index < stack->dns_entry_count; ++index) {
        ch_ip_stack_dns_entry *entry = &stack->dns_entries[index];
        if (entry->address_family == address_family &&
            memcmp(entry->address, address, address_length) == 0) {
            selected = index;
            break;
        }
        if (!ch_ip_stack_dns_entry_alive(entry, now)) {
            selected = index;
            oldest_sequence = 0U;
        } else if (oldest_sequence != 0U &&
                   entry->sequence < oldest_sequence) {
            selected = index;
            oldest_sequence = entry->sequence;
        }
    }
    if (selected == stack->dns_entry_count) {
        if (stack->dns_entry_count < CH_IP_STACK_DNS_CACHE_LIMIT) {
            ch_ip_stack_dns_entry *next = realloc(
                stack->dns_entries,
                (stack->dns_entry_count + 1U) * sizeof(*next));
            if (next == NULL) return;
            stack->dns_entries = next;
            selected = stack->dns_entry_count++;
        } else if (stack->dns_entry_count > 0U) {
            selected = 0U;
            for (size_t index = 1U; index < stack->dns_entry_count; ++index) {
                if (stack->dns_entries[index].sequence <
                    stack->dns_entries[selected].sequence) {
                    selected = index;
                }
            }
        } else {
            return;
        }
    }
    ch_ip_stack_dns_entry *entry = &stack->dns_entries[selected];
    memset(entry, 0, sizeof(*entry));
    entry->address_family = address_family;
    memcpy(entry->address, address, address_length);
    (void)snprintf(entry->domain, sizeof(entry->domain), "%s", domain);
    entry->expires_at = now + (u32_t)ttl_milliseconds;
    entry->sequence = ++stack->dns_sequence;
}

static void ch_ip_stack_dns_cache_response(ch_ip_stack *stack,
                                           const uint8_t *query,
                                           size_t query_length,
                                           const uint8_t *response,
                                           size_t response_length) {
    if (query_length < 12U || response_length < 12U ||
        ch_ip_stack_read_u16(query + 4U) == 0U) {
        return;
    }
    char domain[CH_IP_STACK_DNS_NAME_CAPACITY];
    size_t ignored_next = 0U;
    if (!ch_ip_stack_dns_read_name(query, query_length, 12U, domain,
                                   &ignored_next)) {
        return;
    }
    size_t offset = 12U;
    uint16_t question_count = ch_ip_stack_read_u16(response + 4U);
    for (uint16_t index = 0U; index < question_count; ++index) {
        char ignored_name[CH_IP_STACK_DNS_NAME_CAPACITY];
        if (!ch_ip_stack_dns_read_name(response, response_length, offset,
                                       ignored_name, &offset) ||
            offset + 4U > response_length) {
            return;
        }
        offset += 4U;
    }
    uint16_t answer_count = ch_ip_stack_read_u16(response + 6U);
    for (uint16_t index = 0U; index < answer_count; ++index) {
        char ignored_name[CH_IP_STACK_DNS_NAME_CAPACITY];
        if (!ch_ip_stack_dns_read_name(response, response_length, offset,
                                       ignored_name, &offset) ||
            offset + 10U > response_length) {
            return;
        }
        uint16_t type = ch_ip_stack_read_u16(response + offset);
        uint16_t record_class = ch_ip_stack_read_u16(response + offset + 2U);
        uint32_t ttl = ch_ip_stack_read_u32(response + offset + 4U);
        uint16_t data_length = ch_ip_stack_read_u16(response + offset + 8U);
        offset += 10U;
        if (offset + data_length > response_length) return;
        if (record_class == 1U && type == 1U && data_length == 4U) {
            ch_ip_stack_dns_cache_store(stack, AF_INET, response + offset,
                                        domain, ttl);
        } else if (record_class == 1U && type == 28U &&
                   data_length == 16U) {
            ch_ip_stack_dns_cache_store(stack, AF_INET6, response + offset,
                                        domain, ttl);
        }
        offset += data_length;
    }
}

static const char *ch_ip_stack_dns_cache_lookup(
    ch_ip_stack *stack, int address_family, const uint8_t *address) {
    u32_t now = sys_now();
    size_t address_length = ch_ip_stack_address_length(address_family);
    for (size_t index = 0U; index < stack->dns_entry_count; ++index) {
        ch_ip_stack_dns_entry *entry = &stack->dns_entries[index];
        if (entry->address_family == address_family &&
            ch_ip_stack_dns_entry_alive(entry, now) &&
            memcmp(entry->address, address, address_length) == 0) {
            entry->sequence = ++stack->dns_sequence;
            return entry->domain;
        }
    }
    return NULL;
}

static void ch_ip_stack_domain_hint(ch_ip_stack *stack, int address_family,
                                    const uint8_t *address, uint16_t port,
                                    char hint[CH_IP_STACK_DOMAIN_HINT_CAPACITY]) {
    hint[0] = '\0';
    const char *domain = ch_ip_stack_dns_cache_lookup(
        stack, address_family, address);
    if (domain == NULL) return;
    int length = snprintf(hint, CH_IP_STACK_DOMAIN_HINT_CAPACITY, "%s:%u",
                          domain, (unsigned int)port);
    if (length <= 0 || (size_t)length >= CH_IP_STACK_DOMAIN_HINT_CAPACITY) {
        hint[0] = '\0';
    }
}

static int ch_ip_stack_fragment_covered(const uint8_t *coverage,
                                        size_t offset) {
    return (coverage[offset >> 3U] & (uint8_t)(1U << (offset & 7U))) != 0U;
}

static void ch_ip_stack_fragment_mark(uint8_t *coverage, size_t offset) {
    coverage[offset >> 3U] |= (uint8_t)(1U << (offset & 7U));
}

static ch_ip_stack_fragment_flow *ch_ip_stack_fragment_find(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    const uint8_t *target_address, uint32_t identifier, uint8_t protocol) {
    size_t address_length = ch_ip_stack_address_length(address_family);
    for (ch_ip_stack_fragment_flow *flow = stack->fragment_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->address_family == address_family &&
            flow->identifier == identifier && flow->protocol == protocol &&
            memcmp(flow->source_address, source_address,
                   address_length) == 0 &&
            memcmp(flow->target_address, target_address,
                   address_length) == 0) {
            return flow;
        }
    }
    return NULL;
}

static void ch_ip_stack_fragment_sweep(ch_ip_stack *stack) {
    ch_ip_stack_fragment_flow **cursor = &stack->fragment_flows;
    while (*cursor != NULL) {
        ch_ip_stack_fragment_flow *flow = *cursor;
        if (!flow->dead) {
            cursor = &flow->next;
            continue;
        }
        *cursor = flow->next;
        free(flow->data);
        free(flow->coverage);
        free(flow);
        --stack->fragment_flow_count;
    }
}

static ch_ip_stack_fragment_flow *ch_ip_stack_fragment_create(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    const uint8_t *target_address, uint32_t identifier, uint8_t protocol,
    ch_error *error) {
    if (stack->fragment_flow_count >= CH_IP_STACK_FRAGMENT_FLOW_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native IP fragment flow limit reached");
        return NULL;
    }
    ch_ip_stack_fragment_flow *flow = calloc(1U, sizeof(*flow));
    if (flow == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate IP fragment flow");
        return NULL;
    }
    flow->data = malloc(CH_IP_STACK_FRAGMENT_DATA_CAPACITY);
    flow->coverage = calloc(1U, CH_IP_STACK_FRAGMENT_COVERAGE_BYTES);
    if (flow->data == NULL || flow->coverage == NULL) {
        free(flow->data);
        free(flow->coverage);
        free(flow);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate IP fragment buffers");
        return NULL;
    }
    flow->address_family = address_family;
    flow->identifier = identifier;
    flow->protocol = protocol;
    size_t address_length = ch_ip_stack_address_length(address_family);
    memcpy(flow->source_address, source_address, address_length);
    memcpy(flow->target_address, target_address, address_length);
    flow->last_activity = sys_now();
    flow->next = stack->fragment_flows;
    stack->fragment_flows = flow;
    ++stack->fragment_flow_count;
    return flow;
}

static ch_status ch_ip_stack_fragment_store(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    const uint8_t *target_address, uint32_t identifier, uint8_t protocol,
    const uint8_t *header, size_t header_length,
    size_t ipv6_next_header_offset, size_t fragment_offset,
    const uint8_t *fragment, size_t fragment_length, int more_fragments,
    uint8_t **out_packet, size_t *out_length, ch_error *error) {
    *out_packet = NULL;
    *out_length = 0U;
    if (fragment_offset > CH_IP_STACK_FRAGMENT_DATA_CAPACITY ||
        fragment_length > CH_IP_STACK_FRAGMENT_DATA_CAPACITY -
            fragment_offset ||
        (more_fragments && (fragment_length == 0U ||
                            (fragment_length & 7U) != 0U))) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid IP fragment offset or length");
        return CH_ERROR_PARSE;
    }
    size_t fragment_end = fragment_offset + fragment_length;
    ch_ip_stack_fragment_flow *flow = ch_ip_stack_fragment_find(
        stack, address_family, source_address, target_address, identifier,
        protocol);
    if (flow == NULL) {
        flow = ch_ip_stack_fragment_create(
            stack, address_family, source_address, target_address,
            identifier, protocol, error);
        if (flow == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                                error->code;
    }
    if ((flow->total_known && fragment_end > flow->total_payload_length) ||
        (!more_fragments && flow->total_known &&
         fragment_end != flow->total_payload_length)) {
        flow->dead = 1;
        ch_ip_stack_fragment_sweep(stack);
        ch_error_set(error, CH_ERROR_PARSE,
                     "inconsistent IP fragment total length");
        return CH_ERROR_PARSE;
    }
    for (size_t offset = fragment_offset; offset < fragment_end; ++offset) {
        if (ch_ip_stack_fragment_covered(flow->coverage, offset)) {
            flow->dead = 1;
            ch_ip_stack_fragment_sweep(stack);
            ch_error_set(error, CH_ERROR_PARSE,
                         "overlapping IP fragments are rejected");
            return CH_ERROR_PARSE;
        }
    }
    memcpy(flow->data + fragment_offset, fragment, fragment_length);
    for (size_t offset = fragment_offset; offset < fragment_end; ++offset) {
        ch_ip_stack_fragment_mark(flow->coverage, offset);
    }
    flow->covered_length += fragment_length;
    flow->last_activity = sys_now();
    if (fragment_offset == 0U) {
        if (header_length > sizeof(flow->header)) {
            flow->dead = 1;
            ch_ip_stack_fragment_sweep(stack);
            ch_error_set(error, CH_ERROR_PARSE,
                         "IP fragment header is too large");
            return CH_ERROR_PARSE;
        }
        memcpy(flow->header, header, header_length);
        flow->header_length = header_length;
        flow->ipv6_next_header_offset = ipv6_next_header_offset;
        flow->have_first = 1;
    }
    if (!more_fragments) {
        if (flow->covered_length > fragment_end) {
            flow->dead = 1;
            ch_ip_stack_fragment_sweep(stack);
            ch_error_set(error, CH_ERROR_PARSE,
                         "IP fragments extend beyond final fragment");
            return CH_ERROR_PARSE;
        }
        flow->total_payload_length = fragment_end;
        flow->total_known = 1;
    }
    if (!flow->have_first || !flow->total_known ||
        flow->covered_length != flow->total_payload_length) {
        return CH_OK;
    }
    size_t packet_length = flow->header_length + flow->total_payload_length;
    if (packet_length > CH_IP_STACK_MAX_PACKET) {
        flow->dead = 1;
        ch_ip_stack_fragment_sweep(stack);
        ch_error_set(error, CH_ERROR_PARSE,
                     "reassembled IP packet is too large");
        return CH_ERROR_PARSE;
    }
    uint8_t *packet = malloc(packet_length);
    if (packet == NULL) {
        flow->dead = 1;
        ch_ip_stack_fragment_sweep(stack);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate reassembled IP packet");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(packet, flow->header, flow->header_length);
    memcpy(packet + flow->header_length, flow->data,
           flow->total_payload_length);
    if (address_family == AF_INET) {
        ch_ip_stack_write_u16(packet + 2U, (uint16_t)packet_length);
        uint16_t flags = ch_ip_stack_read_u16(packet + 6U) & 0x4000U;
        ch_ip_stack_write_u16(packet + 6U, flags);
        packet[10] = 0U;
        packet[11] = 0U;
        ch_ip_stack_write_u16(packet + 10U,
                              ch_ip_stack_checksum(packet,
                                                   flow->header_length));
    } else {
        if (flow->ipv6_next_header_offset >= flow->header_length) {
            free(packet);
            flow->dead = 1;
            ch_ip_stack_fragment_sweep(stack);
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "invalid saved IPv6 extension chain");
            return CH_ERROR_INTERNAL;
        }
        packet[flow->ipv6_next_header_offset] = flow->protocol;
        ch_ip_stack_write_u16(packet + 4U,
                              (uint16_t)(packet_length - 40U));
    }
    flow->dead = 1;
    ch_ip_stack_fragment_sweep(stack);
    *out_packet = packet;
    *out_length = packet_length;
    return CH_OK;
}

static ch_status ch_ip_stack_prepare_input_packet(
    ch_ip_stack *stack, const uint8_t *packet, size_t length,
    uint8_t **out_packet, size_t *out_length, ch_error *error) {
    *out_packet = NULL;
    *out_length = 0U;
    unsigned int version = packet[0] >> 4U;
    if (version == 4U) {
        size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
        if (header_length < 20U || header_length > length ||
            ch_ip_stack_read_u16(packet + 2U) != length ||
            ch_ip_stack_checksum(packet, header_length) != 0U) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "invalid IPv4 packet header");
            return CH_ERROR_PARSE;
        }
        uint16_t fragment_field = ch_ip_stack_read_u16(packet + 6U);
        size_t fragment_offset = (size_t)(fragment_field & 0x1fffU) * 8U;
        int more_fragments = (fragment_field & 0x2000U) != 0U;
        if (fragment_offset == 0U && !more_fragments) {
            *out_packet = malloc(length);
            if (*out_packet != NULL) {
                memcpy(*out_packet, packet, length);
                *out_length = length;
                return CH_OK;
            }
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy IP packet input");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        return ch_ip_stack_fragment_store(
            stack, AF_INET, packet + 12U, packet + 16U,
            ch_ip_stack_read_u16(packet + 4U), packet[9], packet,
            header_length, 0U, fragment_offset, packet + header_length,
            length - header_length, more_fragments, out_packet, out_length,
            error);
    }
    if (length < 40U || ch_ip_stack_read_u16(packet + 4U) != length - 40U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv6 packet length");
        return CH_ERROR_PARSE;
    }
    uint8_t next_header = packet[6];
    size_t cursor = 40U;
    size_t next_header_offset = 6U;
    unsigned int extension_count = 0U;
    while (next_header == 0U || next_header == 43U ||
           next_header == 60U || next_header == 51U) {
        if (++extension_count > 16U || cursor + 2U > length) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "invalid IPv6 extension header chain");
            return CH_ERROR_PARSE;
        }
        size_t extension_length = next_header == 51U ?
            ((size_t)packet[cursor + 1U] + 2U) * 4U :
            ((size_t)packet[cursor + 1U] + 1U) * 8U;
        if (extension_length < 8U || cursor + extension_length > length ||
            cursor + extension_length > CH_IP_STACK_EXTENSION_HEADER_LIMIT) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "invalid or excessive IPv6 extension header");
            return CH_ERROR_PARSE;
        }
        next_header_offset = cursor;
        next_header = packet[cursor];
        cursor += extension_length;
    }
    if (next_header != 44U) {
        *out_packet = malloc(length);
        if (*out_packet != NULL) {
            memcpy(*out_packet, packet, length);
            *out_length = length;
            return CH_OK;
        }
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy IP packet input");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (cursor + 8U > length || packet[cursor + 1U] != 0U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv6 fragment header");
        return CH_ERROR_PARSE;
    }
    uint16_t fragment_field = ch_ip_stack_read_u16(packet + cursor + 2U);
    if ((fragment_field & 0x0006U) != 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid IPv6 fragment reserved bits");
        return CH_ERROR_PARSE;
    }
    return ch_ip_stack_fragment_store(
        stack, AF_INET6, packet + 8U, packet + 24U,
        ch_ip_stack_read_u32(packet + cursor + 4U), packet[cursor], packet,
        cursor, next_header_offset, fragment_field & 0xfff8U,
        packet + cursor + 8U, length - cursor - 8U,
        (fragment_field & 1U) != 0U, out_packet, out_length, error);
}

static uint16_t ch_ip_stack_udp_checksum_ipv4(const uint8_t *packet,
                                              size_t length) {
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    if (header_length < 20U || length < header_length + 8U) return UINT16_MAX;
    size_t udp_length = length - header_length;
    uint32_t sum = ch_ip_stack_checksum_add(packet + 12U, 8U, 0U);
    uint8_t pseudo[4] = {
        0U, 17U, (uint8_t)(udp_length >> 8U), (uint8_t)udp_length
    };
    sum = ch_ip_stack_checksum_add(pseudo, sizeof(pseudo), sum);
    sum = ch_ip_stack_checksum_add(packet + header_length, udp_length, sum);
    return ch_ip_stack_checksum_finish(sum);
}

static uint16_t ch_ip_stack_udp_checksum_ipv6(const uint8_t *packet,
                                              size_t length) {
    uint8_t protocol = 0U;
    size_t udp_offset = 0U;
    if (!ch_ip_stack_ipv6_transport(packet, length, &protocol, &udp_offset) ||
        protocol != 17U || length < udp_offset + 8U) {
        return UINT16_MAX;
    }
    size_t udp_length = length - udp_offset;
    uint32_t sum = ch_ip_stack_checksum_add(packet + 8U, 32U, 0U);
    uint8_t pseudo[8] = {
        (uint8_t)(udp_length >> 24U),
        (uint8_t)(udp_length >> 16U),
        (uint8_t)(udp_length >> 8U),
        (uint8_t)udp_length,
        0U, 0U, 0U, 17U
    };
    sum = ch_ip_stack_checksum_add(pseudo, sizeof(pseudo), sum);
    sum = ch_ip_stack_checksum_add(packet + udp_offset, udp_length, sum);
    return ch_ip_stack_checksum_finish(sum);
}

static ch_status ch_ip_stack_emit_udp_response(
    ch_ip_stack *stack, int address_family, const uint8_t *remote_address,
    uint16_t remote_port, const uint8_t *client_address, uint16_t client_port,
    const uint8_t *payload, size_t payload_length, ch_error *error) {
    size_t ip_header_length = address_family == AF_INET6 ? 40U : 20U;
    size_t packet_length = ip_header_length + 8U + payload_length;
    if (payload_length > UINT16_MAX - 8U || packet_length > stack->mtu ||
        packet_length > CH_IP_STACK_MAX_PACKET) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "UDP TUN response exceeds the configured MTU");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *packet = calloc(1U, packet_length);
    if (packet == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate UDP TUN response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t udp_offset;
    if (address_family == AF_INET6) {
        packet[0] = 0x60U;
        ch_ip_stack_write_u16(packet + 4U,
                              (uint16_t)(8U + payload_length));
        packet[6] = 17U;
        packet[7] = 64U;
        memcpy(packet + 8U, remote_address, 16U);
        memcpy(packet + 24U, client_address, 16U);
        udp_offset = 40U;
    } else {
        packet[0] = 0x45U;
        ch_ip_stack_write_u16(packet + 2U, (uint16_t)packet_length);
        uint32_t identifier = ch_lwip_random();
        ch_ip_stack_write_u16(packet + 4U, (uint16_t)identifier);
        packet[6] = 0x40U;
        packet[8] = 64U;
        packet[9] = 17U;
        memcpy(packet + 12U, remote_address, 4U);
        memcpy(packet + 16U, client_address, 4U);
        udp_offset = 20U;
    }
    ch_ip_stack_write_u16(packet + udp_offset, remote_port);
    ch_ip_stack_write_u16(packet + udp_offset + 2U, client_port);
    ch_ip_stack_write_u16(packet + udp_offset + 4U,
                          (uint16_t)(8U + payload_length));
    if (payload_length > 0U) {
        memcpy(packet + udp_offset + 8U, payload, payload_length);
    }
    uint16_t udp_checksum = address_family == AF_INET6 ?
        ch_ip_stack_udp_checksum_ipv6(packet, packet_length) :
        ch_ip_stack_udp_checksum_ipv4(packet, packet_length);
    if (udp_checksum == 0U) udp_checksum = UINT16_MAX;
    ch_ip_stack_write_u16(packet + udp_offset + 6U, udp_checksum);
    if (address_family == AF_INET) {
        ch_ip_stack_write_u16(packet + 10U,
                              ch_ip_stack_checksum(packet, 20U));
    }
    stack->packet_writer(packet, packet_length, stack->packet_writer_context);
    free(packet);
    return CH_OK;
}

static ch_ip_stack_tcp_flow *ch_ip_stack_tcp_flow_for_input(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    uint16_t source_port,
    const uint8_t *target_address, uint16_t target_port) {
    size_t address_length = ch_ip_stack_address_length(address_family);
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->address_family == address_family &&
            flow->source_port == source_port &&
            flow->target_port == target_port &&
            memcmp(flow->source_address, source_address, address_length) == 0 &&
            memcmp(flow->target_address, target_address, address_length) == 0) {
            return flow;
        }
    }
    return NULL;
}

static ch_ip_stack_tcp_flow *ch_ip_stack_tcp_flow_for_output(
    ch_ip_stack *stack, int address_family, const uint8_t *client_address,
    uint16_t client_port, uint16_t internal_port) {
    size_t address_length = ch_ip_stack_address_length(address_family);
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->address_family == address_family &&
            flow->source_port == client_port &&
            flow->internal_port == internal_port &&
            memcmp(flow->source_address, client_address, address_length) == 0) {
            return flow;
        }
    }
    return NULL;
}

static void ch_ip_stack_tcp_close_descriptor(ch_ip_stack_tcp_flow *flow) {
    if (flow->descriptor < 0) return;
    (void)shutdown(flow->descriptor, SHUT_RDWR);
    (void)close(flow->descriptor);
    flow->descriptor = -1;
}

static void ch_ip_stack_observe_bytes(ch_ip_stack *stack, uint64_t flow_id,
                                      uint64_t rx_delta,
                                      uint64_t tx_delta) {
    if (stack->flow_bytes != NULL && flow_id != 0U &&
        (rx_delta != 0U || tx_delta != 0U)) {
        stack->flow_bytes(flow_id, rx_delta, tx_delta,
                          stack->flow_observer_context);
    }
}

static void ch_ip_stack_tcp_observe_close(ch_ip_stack_tcp_flow *flow,
                                          const char *reason) {
    if (flow == NULL || flow->close_observed) return;
    flow->close_observed = 1;
    if (flow->stack->flow_close != NULL && flow->flow_id != 0U) {
        flow->stack->flow_close(flow->flow_id, reason,
                                flow->stack->flow_observer_context);
    }
}

static void ch_ip_stack_udp_observe_close(ch_ip_stack_udp_flow *flow,
                                          const char *reason) {
    if (flow == NULL || flow->close_observed) return;
    flow->close_observed = 1;
    if (flow->stack->flow_close != NULL && flow->flow_id != 0U) {
        flow->stack->flow_close(flow->flow_id, reason,
                                flow->stack->flow_observer_context);
    }
}

static int ch_ip_stack_set_nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL, 0);
    if (flags < 0 || fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) != 0) {
        return 0;
    }
#ifdef SO_NOSIGPIPE
    int enabled = 1;
    (void)setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &enabled,
                     (socklen_t)sizeof(enabled));
#endif
    return 1;
}

static ssize_t ch_ip_stack_send(int descriptor, const void *bytes,
                                size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static void ch_ip_stack_tcp_error(void *context, err_t error) {
    (void)error;
    ch_ip_stack_tcp_flow *flow = context;
    if (flow == NULL) return;
    flow->client = NULL;
    flow->dead = 1;
    ch_ip_stack_tcp_observe_close(flow, "TCP stack error");
    ch_ip_stack_tcp_close_descriptor(flow);
}

static err_t ch_ip_stack_tcp_receive(void *context, struct tcp_pcb *client,
                                     struct pbuf *packet, err_t error) {
    ch_ip_stack_tcp_flow *flow = context;
    if (flow == NULL || flow->dead || error != ERR_OK) {
        if (packet != NULL) (void)pbuf_free(packet);
        return error == ERR_OK ? ERR_ABRT : error;
    }
    if (packet == NULL) {
        flow->client_eof = 1;
        if (flow->descriptor >= 0) (void)shutdown(flow->descriptor, SHUT_WR);
        return ERR_OK;
    }
    if (packet->tot_len == 0U) {
        (void)pbuf_free(packet);
        return ERR_OK;
    }
    size_t queued = flow->pending_length - flow->pending_offset;
    if (packet->tot_len > CH_IP_STACK_TCP_QUEUE_LIMIT - queued) {
        return ERR_MEM;
    }
    uint8_t *next = malloc(queued + packet->tot_len);
    if (next == NULL) return ERR_MEM;
    if (queued > 0U) {
        memcpy(next, flow->pending + flow->pending_offset, queued);
    }
    u16_t copied = pbuf_copy_partial(packet, next + queued, packet->tot_len,
                                     0U);
    if (copied != packet->tot_len) {
        free(next);
        return ERR_BUF;
    }
    free(flow->pending);
    flow->pending = next;
    flow->pending_offset = 0U;
    flow->pending_length = queued + packet->tot_len;
    tcp_recved(client, packet->tot_len);
    (void)pbuf_free(packet);
    return ERR_OK;
}

static err_t ch_ip_stack_tcp_poll(void *context, struct tcp_pcb *client) {
    (void)client;
    ch_ip_stack_tcp_flow *flow = context;
    return flow == NULL || flow->dead ? ERR_ABRT : ERR_OK;
}

static err_t ch_ip_stack_tcp_accept(void *context, struct tcp_pcb *client,
                                    err_t error) {
    ch_ip_stack_tcp_flow *flow = context;
    if (flow == NULL || flow->dead || client == NULL || error != ERR_OK) {
        if (client != NULL) tcp_abort(client);
        return ERR_ABRT;
    }
    flow->client = client;
    struct tcp_pcb *listener = flow->listener;
    flow->listener = NULL;
    if (listener != NULL) {
        tcp_arg(listener, NULL);
        tcp_accept(listener, NULL);
        if (tcp_close(listener) != ERR_OK) tcp_abort(listener);
    }
    tcp_arg(client, flow);
    tcp_recv(client, ch_ip_stack_tcp_receive);
    tcp_err(client, ch_ip_stack_tcp_error);
    tcp_poll(client, ch_ip_stack_tcp_poll, 2U);
    char target_address[INET6_ADDRSTRLEN];
    char source_address[INET6_ADDRSTRLEN];
    if (inet_ntop(flow->address_family, flow->target_address, target_address,
                  sizeof(target_address)) == NULL ||
        inet_ntop(flow->address_family, flow->source_address, source_address,
                  sizeof(source_address)) == NULL) {
        flow->dead = 1;
        tcp_abort(client);
        return ERR_ABRT;
    }
    char target[INET6_ADDRSTRLEN + 10U];
    char source[INET6_ADDRSTRLEN + 10U];
    (void)snprintf(target, sizeof(target),
                   flow->address_family == AF_INET6 ? "[%s]:%u" : "%s:%u",
                   target_address, (unsigned int)flow->target_port);
    (void)snprintf(source, sizeof(source),
                   flow->address_family == AF_INET6 ? "[%s]:%u" : "%s:%u",
                   source_address, (unsigned int)flow->source_port);
    ch_error dial_error;
    int descriptor = -1;
    uint64_t flow_id = 0U;
    ch_status status = flow->stack->tcp_dialer(
        target, source, flow->domain_hint, &descriptor, &flow_id,
        flow->stack->tcp_dialer_context, &dial_error);
    if (status != CH_OK || descriptor < 0 ||
        !ch_ip_stack_set_nonblocking(descriptor)) {
        if (descriptor >= 0) (void)close(descriptor);
        flow->dead = 1;
        tcp_abort(client);
        return ERR_ABRT;
    }
    flow->descriptor = descriptor;
    flow->flow_id = flow_id;
    return ERR_OK;
}

static int ch_ip_stack_tcp_port_in_use(const ch_ip_stack *stack,
                                       uint16_t port) {
    for (const ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->internal_port == port) return 1;
    }
    return 0;
}

static ch_ip_stack_tcp_flow *ch_ip_stack_tcp_flow_create(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    uint16_t source_port,
    const uint8_t *target_address, uint16_t target_port, ch_error *error) {
    if (stack->tcp_flow_count >= CH_IP_STACK_TCP_FLOW_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native TCP TUN flow limit reached");
        return NULL;
    }
    ch_ip_stack_tcp_flow *flow = calloc(1U, sizeof(*flow));
    if (flow == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate TCP TUN flow");
        return NULL;
    }
    flow->stack = stack;
    flow->descriptor = -1;
    flow->address_family = address_family;
    size_t address_length = ch_ip_stack_address_length(address_family);
    memcpy(flow->source_address, source_address, address_length);
    memcpy(flow->target_address, target_address, address_length);
    flow->source_port = source_port;
    flow->target_port = target_port;
    flow->created_at = sys_now();
    ch_ip_stack_domain_hint(stack, address_family, target_address,
                            target_port, flow->domain_hint);
    struct tcp_pcb *pcb = NULL;
    err_t bind_error = ERR_USE;
    for (uint32_t attempt = 0U;
         attempt <= CH_IP_STACK_TCP_LAST_PORT - CH_IP_STACK_TCP_FIRST_PORT;
         ++attempt) {
        uint16_t port = stack->next_tcp_port;
        ++stack->next_tcp_port;
        if (stack->next_tcp_port > CH_IP_STACK_TCP_LAST_PORT) {
            stack->next_tcp_port = CH_IP_STACK_TCP_FIRST_PORT;
        }
        if (ch_ip_stack_tcp_port_in_use(stack, port)) continue;
        pcb = tcp_new_ip_type(address_family == AF_INET6 ? IPADDR_TYPE_V6 :
                                                          IPADDR_TYPE_V4);
        if (pcb == NULL) {
            bind_error = ERR_MEM;
            break;
        }
        ip_addr_t local;
        if (address_family == AF_INET6) {
            ip_addr_copy_from_ip6(
                local, *netif_ip6_addr(&stack->interface, stack->ipv6_index));
        } else {
            ip_addr_copy_from_ip4(local, *netif_ip4_addr(&stack->interface));
        }
        bind_error = tcp_bind(pcb, &local, port);
        if (bind_error == ERR_OK) {
            flow->internal_port = port;
            break;
        }
        tcp_abort(pcb);
        pcb = NULL;
    }
    if (pcb == NULL || bind_error != ERR_OK) {
        if (pcb != NULL) tcp_abort(pcb);
        free(flow);
        ch_error_set(error, bind_error == ERR_MEM ? CH_ERROR_OUT_OF_MEMORY :
                                                   CH_ERROR_INVALID_STATE,
                     "allocate internal TCP TUN listener");
        return NULL;
    }
    tcp_arg(pcb, flow);
    err_t listen_error = ERR_OK;
    struct tcp_pcb *listener = tcp_listen_with_backlog_and_err(
        pcb, 1U, &listen_error);
    if (listener == NULL || listen_error != ERR_OK) {
        if (listener != NULL) tcp_abort(listener);
        else tcp_abort(pcb);
        free(flow);
        ch_error_set(error, listen_error == ERR_MEM ? CH_ERROR_OUT_OF_MEMORY :
                                                     CH_ERROR_INTERNAL,
                     "listen for internal TCP TUN flow");
        return NULL;
    }
    flow->listener = listener;
    tcp_arg(listener, flow);
    tcp_accept(listener, ch_ip_stack_tcp_accept);
    flow->next = stack->tcp_flows;
    stack->tcp_flows = flow;
    ++stack->tcp_flow_count;
    return flow;
}

static void ch_ip_stack_tcp_flow_abort(ch_ip_stack_tcp_flow *flow) {
    if (flow == NULL) return;
    struct tcp_pcb *active = tcp_active_pcbs;
    while (active != NULL) {
        struct tcp_pcb *next = active->next;
        if (active != flow->client && active->callback_arg == flow &&
            active->state == SYN_RCVD) {
            tcp_abort(active);
        }
        active = next;
    }
    if (flow->listener != NULL) {
        tcp_arg(flow->listener, NULL);
        tcp_accept(flow->listener, NULL);
        if (tcp_close(flow->listener) != ERR_OK) tcp_abort(flow->listener);
        flow->listener = NULL;
    }
    if (flow->client != NULL) {
        tcp_arg(flow->client, NULL);
        tcp_recv(flow->client, NULL);
        tcp_err(flow->client, NULL);
        tcp_poll(flow->client, NULL, 0U);
        tcp_abort(flow->client);
        flow->client = NULL;
    }
    ch_ip_stack_tcp_close_descriptor(flow);
    flow->dead = 1;
    ch_ip_stack_tcp_observe_close(flow, "TCP flow aborted");
}

static void ch_ip_stack_tcp_flow_tick(ch_ip_stack_tcp_flow *flow) {
    if (flow->dead) return;
    if (flow->client == NULL &&
        (u32_t)(sys_now() - flow->created_at) >=
            CH_IP_STACK_TCP_HANDSHAKE_TIMEOUT_MS) {
        ch_ip_stack_tcp_flow_abort(flow);
        return;
    }
    if (flow->descriptor < 0 || flow->client == NULL) return;
    while (flow->pending_offset < flow->pending_length) {
        ssize_t written = ch_ip_stack_send(
            flow->descriptor, flow->pending + flow->pending_offset,
            flow->pending_length - flow->pending_offset);
        if (written > 0) {
            flow->pending_offset += (size_t)written;
            ch_ip_stack_observe_bytes(flow->stack, flow->flow_id, 0U,
                                      (uint64_t)written);
            continue;
        }
        if (written < 0 && errno == EINTR) continue;
        if (written < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) break;
        ch_ip_stack_tcp_flow_abort(flow);
        return;
    }
    if (flow->pending_offset == flow->pending_length) {
        free(flow->pending);
        flow->pending = NULL;
        flow->pending_offset = 0U;
        flow->pending_length = 0U;
    }
    if (flow->remote_pending_length > 0U) {
        u16_t available = tcp_sndbuf(flow->client);
        if (available == 0U) return;
        size_t amount = flow->remote_pending_length < available ?
            flow->remote_pending_length : available;
        err_t write_error = tcp_write(
            flow->client, flow->remote_pending, (u16_t)amount,
            TCP_WRITE_FLAG_COPY);
        if (write_error == ERR_MEM) return;
        if (write_error != ERR_OK || tcp_output(flow->client) != ERR_OK) {
            ch_ip_stack_tcp_flow_abort(flow);
            return;
        }
        if (amount < flow->remote_pending_length) {
            memmove(flow->remote_pending, flow->remote_pending + amount,
                    flow->remote_pending_length - amount);
            flow->remote_pending_length -= amount;
            return;
        }
        free(flow->remote_pending);
        flow->remote_pending = NULL;
        flow->remote_pending_length = 0U;
    }
    for (unsigned int attempt = 0U; attempt < 4U && !flow->remote_eof;
         ++attempt) {
        u16_t available = tcp_sndbuf(flow->client);
        if (available == 0U) break;
        size_t capacity = available < CH_IP_STACK_TCP_READ_SIZE ? available :
                                                                CH_IP_STACK_TCP_READ_SIZE;
        uint8_t buffer[CH_IP_STACK_TCP_READ_SIZE];
        ssize_t received = recv(flow->descriptor, buffer, capacity, 0);
        if (received > 0) {
            err_t write_error = tcp_write(
                flow->client, buffer, (u16_t)received, TCP_WRITE_FLAG_COPY);
            if (write_error == ERR_MEM) {
                flow->remote_pending = malloc((size_t)received);
                if (flow->remote_pending == NULL) {
                    ch_ip_stack_tcp_flow_abort(flow);
                    return;
                }
                memcpy(flow->remote_pending, buffer, (size_t)received);
                flow->remote_pending_length = (size_t)received;
                break;
            }
            if (write_error != ERR_OK || tcp_output(flow->client) != ERR_OK) {
                ch_ip_stack_tcp_flow_abort(flow);
                return;
            }
            ch_ip_stack_observe_bytes(flow->stack, flow->flow_id,
                                      (uint64_t)received, 0U);
            continue;
        }
        if (received == 0) {
            flow->remote_eof = 1;
            if (tcp_shutdown(flow->client, 0, 1) != ERR_OK) {
                ch_ip_stack_tcp_flow_abort(flow);
                return;
            }
            break;
        }
        if (errno == EINTR) {
            --attempt;
            continue;
        }
        if (errno == EAGAIN || errno == EWOULDBLOCK) break;
        ch_ip_stack_tcp_flow_abort(flow);
        return;
    }
    if (flow->client_eof && flow->remote_eof &&
        flow->pending_length == 0U && flow->remote_pending_length == 0U &&
        flow->client != NULL) {
        tcp_arg(flow->client, NULL);
        tcp_recv(flow->client, NULL);
        tcp_err(flow->client, NULL);
        tcp_poll(flow->client, NULL, 0U);
        if (tcp_close(flow->client) == ERR_OK) {
            flow->client = NULL;
            flow->dead = 1;
            ch_ip_stack_tcp_observe_close(flow, "closed");
            ch_ip_stack_tcp_close_descriptor(flow);
        } else {
            tcp_arg(flow->client, flow);
            tcp_recv(flow->client, ch_ip_stack_tcp_receive);
            tcp_err(flow->client, ch_ip_stack_tcp_error);
            tcp_poll(flow->client, ch_ip_stack_tcp_poll, 2U);
        }
    }
}

static void ch_ip_stack_tcp_sweep(ch_ip_stack *stack) {
    ch_ip_stack_tcp_flow **cursor = &stack->tcp_flows;
    while (*cursor != NULL) {
        ch_ip_stack_tcp_flow *flow = *cursor;
        if (!flow->dead) {
            cursor = &flow->next;
            continue;
        }
        *cursor = flow->next;
        ch_ip_stack_tcp_observe_close(flow, "closed");
        ch_ip_stack_tcp_close_descriptor(flow);
        free(flow->pending);
        free(flow->remote_pending);
        free(flow);
        --stack->tcp_flow_count;
    }
}

static ch_ip_stack_udp_flow *ch_ip_stack_udp_flow_find(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    uint16_t source_port, const uint8_t *target_address,
    uint16_t target_port) {
    size_t address_length = ch_ip_stack_address_length(address_family);
    for (ch_ip_stack_udp_flow *flow = stack->udp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->address_family == address_family &&
            flow->source_port == source_port &&
            flow->target_port == target_port &&
            memcmp(flow->source_address, source_address,
                   address_length) == 0 &&
            memcmp(flow->target_address, target_address,
                   address_length) == 0) {
            return flow;
        }
    }
    return NULL;
}

static ch_ip_stack_udp_flow *ch_ip_stack_udp_flow_create(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    uint16_t source_port, const uint8_t *target_address,
    uint16_t target_port, ch_error *error) {
    if (stack->udp_flow_count >= CH_IP_STACK_UDP_FLOW_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native UDP TUN flow limit reached");
        return NULL;
    }
    ch_ip_stack_udp_flow *flow = calloc(1U, sizeof(*flow));
    if (flow == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate UDP TUN flow");
        return NULL;
    }
    flow->stack = stack;
    flow->address_family = address_family;
    flow->source_port = source_port;
    flow->target_port = target_port;
    ch_ip_stack_domain_hint(stack, address_family, target_address,
                            target_port, flow->domain_hint);
    size_t address_length = ch_ip_stack_address_length(address_family);
    memcpy(flow->source_address, source_address, address_length);
    memcpy(flow->target_address, target_address, address_length);
    char target[INET6_ADDRSTRLEN + 10U];
    char source[INET6_ADDRSTRLEN + 10U];
    if (!ch_ip_stack_format_endpoint(address_family, target_address,
                                     target_port, target) ||
        !ch_ip_stack_format_endpoint(address_family, source_address,
                                     source_port, source)) {
        free(flow);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "format UDP TUN route endpoints");
        return NULL;
    }
    uint64_t flow_id = 0U;
    ch_status status = stack->udp_dialer(
        target, source, flow->domain_hint, &flow->connection, &flow_id,
        stack->udp_dialer_context, error);
    if (status != CH_OK || flow->connection == NULL) {
        if (flow->connection != NULL) stack->udp_closer(flow->connection);
        free(flow);
        if (status == CH_OK) {
            ch_error_set(error, CH_ERROR_INVALID_STATE,
                         "UDP TUN dialer returned no connection");
        }
        return NULL;
    }
    flow->flow_id = flow_id;
    flow->last_activity = sys_now();
    flow->next = stack->udp_flows;
    stack->udp_flows = flow;
    ++stack->udp_flow_count;
    return flow;
}

static void ch_ip_stack_udp_flow_tick(ch_ip_stack_udp_flow *flow,
                                      uint8_t *buffer,
                                      size_t buffer_capacity) {
    if (flow->dead || flow->connection == NULL) return;
    for (unsigned int attempt = 0U; attempt < 4U; ++attempt) {
        size_t payload_length = 0U;
        char *source = NULL;
        ch_error receive_error;
        ch_status status = flow->stack->udp_receiver(
            flow->connection, buffer, buffer_capacity, &payload_length,
            &source, &receive_error);
        if (status == CH_ERROR_NOT_FOUND) {
            free(source);
            break;
        }
        if (status != CH_OK || source == NULL ||
            payload_length > buffer_capacity) {
            free(source);
            flow->dead = 1;
            break;
        }
        uint8_t remote_address[16];
        uint16_t remote_port = 0U;
        if (!ch_ip_stack_parse_endpoint(source, flow->address_family,
                                        remote_address, &remote_port)) {
            free(source);
            flow->dead = 1;
            break;
        }
        free(source);
        ch_error emit_error;
        if (ch_ip_stack_emit_udp_response(
                flow->stack, flow->address_family, remote_address,
                remote_port, flow->source_address, flow->source_port,
                buffer, payload_length, &emit_error) != CH_OK) {
            flow->dead = 1;
            break;
        }
        ch_ip_stack_observe_bytes(flow->stack, flow->flow_id,
                                  (uint64_t)payload_length, 0U);
        flow->last_activity = sys_now();
    }
    if ((u32_t)(sys_now() - flow->last_activity) >=
        CH_IP_STACK_UDP_FLOW_TIMEOUT_MS) {
        flow->dead = 1;
    }
}

static void ch_ip_stack_udp_sweep(ch_ip_stack *stack) {
    ch_ip_stack_udp_flow **cursor = &stack->udp_flows;
    while (*cursor != NULL) {
        ch_ip_stack_udp_flow *flow = *cursor;
        if (!flow->dead) {
            cursor = &flow->next;
            continue;
        }
        *cursor = flow->next;
        ch_ip_stack_udp_observe_close(flow, "closed");
        if (flow->connection != NULL) stack->udp_closer(flow->connection);
        free(flow);
        --stack->udp_flow_count;
    }
}

static void ch_ip_stack_rewrite_tcp_output(ch_ip_stack *stack,
                                           uint8_t *packet, size_t length) {
    unsigned int version = packet[0] >> 4U;
    if (version == 6U) {
        uint8_t protocol = 0U;
        size_t tcp_offset = 0U;
        if (!ch_ip_stack_ipv6_transport(
                packet, length, &protocol, &tcp_offset) ||
            protocol != 6U || length < tcp_offset + 20U) {
            return;
        }
        uint16_t internal_port = ch_ip_stack_read_u16(packet + tcp_offset);
        uint16_t client_port = ch_ip_stack_read_u16(packet + tcp_offset + 2U);
        ch_ip_stack_tcp_flow *flow = ch_ip_stack_tcp_flow_for_output(
            stack, AF_INET6, packet + 24U, client_port, internal_port);
        if (flow == NULL) return;
        memcpy(packet + 8U, flow->target_address, 16U);
        ch_ip_stack_write_u16(packet + tcp_offset, flow->target_port);
        ch_ip_stack_ipv6_tcp_checksum(packet, length);
        return;
    }
    if (length < 40U || version != 4U || packet[9] != 6U) return;
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    if (header_length < 20U || length < header_length + 20U) return;
    uint16_t internal_port = ch_ip_stack_read_u16(packet + header_length);
    uint16_t client_port = ch_ip_stack_read_u16(packet + header_length + 2U);
    ch_ip_stack_tcp_flow *flow = ch_ip_stack_tcp_flow_for_output(
        stack, AF_INET, packet + 16U, client_port, internal_port);
    if (flow == NULL) return;
    memcpy(packet + 12U, flow->target_address, 4U);
    ch_ip_stack_write_u16(packet + header_length, flow->target_port);
    ch_ip_stack_ipv4_checksums(packet, length);
}

static ch_status ch_ip_stack_prepare_ipv4_tcp_input(
    ch_ip_stack *stack, uint8_t *packet, size_t length, ch_error *error) {
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    uint16_t total_length = ch_ip_stack_read_u16(packet + 2U);
    if (header_length < 20U || header_length > length ||
        total_length < header_length || total_length != length) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv4 packet length");
        return CH_ERROR_PARSE;
    }
    if (packet[9] != 6U ||
        memcmp(packet + 16U, netif_ip4_addr(&stack->interface), 4U) == 0) {
        return CH_OK;
    }
    if (stack->tcp_dialer == NULL) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "IPv4 TCP forwarding has no route dialer");
        return CH_ERROR_UNSUPPORTED;
    }
    uint16_t fragment = ch_ip_stack_read_u16(packet + 6U);
    if ((fragment & 0x3fffU) != 0U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "fragmented IPv4 TCP forwarding is not implemented");
        return CH_ERROR_UNSUPPORTED;
    }
    if (length < header_length + 20U) {
        ch_error_set(error, CH_ERROR_PARSE, "truncated IPv4 TCP packet");
        return CH_ERROR_PARSE;
    }
    uint8_t *tcp = packet + header_length;
    size_t tcp_header_length = (size_t)(tcp[12] >> 4U) * 4U;
    if (tcp_header_length < 20U || header_length + tcp_header_length > length) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv4 TCP header");
        return CH_ERROR_PARSE;
    }
    uint16_t source_port = ch_ip_stack_read_u16(tcp);
    uint16_t target_port = ch_ip_stack_read_u16(tcp + 2U);
    ch_ip_stack_tcp_flow *flow = ch_ip_stack_tcp_flow_for_input(
        stack, AF_INET, packet + 12U, source_port, packet + 16U, target_port);
    if (flow == NULL) {
        int syn = (tcp[13] & 0x02U) != 0U;
        int ack = (tcp[13] & 0x10U) != 0U;
        if (!syn || ack) return CH_OK;
        flow = ch_ip_stack_tcp_flow_create(
            stack, AF_INET, packet + 12U, source_port, packet + 16U,
            target_port, error);
        if (flow == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                                error->code;
    }
    memcpy(packet + 16U, netif_ip4_addr(&stack->interface), 4U);
    ch_ip_stack_write_u16(tcp + 2U, flow->internal_port);
    ch_ip_stack_ipv4_checksums(packet, length);
    return CH_OK;
}

static ch_status ch_ip_stack_prepare_ipv6_tcp_input(
    ch_ip_stack *stack, uint8_t *packet, size_t length, ch_error *error) {
    if (length < 40U || ch_ip_stack_read_u16(packet + 4U) != length - 40U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv6 packet length");
        return CH_ERROR_PARSE;
    }
    uint8_t protocol = 0U;
    size_t tcp_offset = 0U;
    if (!ch_ip_stack_ipv6_transport(
            packet, length, &protocol, &tcp_offset)) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid IPv6 extension header chain");
        return CH_ERROR_PARSE;
    }
    if (protocol != 6U ||
        memcmp(packet + 24U,
               netif_ip6_addr(&stack->interface, stack->ipv6_index),
               16U) == 0) {
        return CH_OK;
    }
    if (stack->tcp_dialer == NULL) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "IPv6 TCP forwarding has no route dialer");
        return CH_ERROR_UNSUPPORTED;
    }
    if (length < tcp_offset + 20U) {
        ch_error_set(error, CH_ERROR_PARSE, "truncated IPv6 TCP packet");
        return CH_ERROR_PARSE;
    }
    uint8_t *tcp = packet + tcp_offset;
    size_t tcp_header_length = (size_t)(tcp[12] >> 4U) * 4U;
    if (tcp_header_length < 20U ||
        tcp_offset + tcp_header_length > length) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv6 TCP header");
        return CH_ERROR_PARSE;
    }
    uint16_t source_port = ch_ip_stack_read_u16(tcp);
    uint16_t target_port = ch_ip_stack_read_u16(tcp + 2U);
    ch_ip_stack_tcp_flow *flow = ch_ip_stack_tcp_flow_for_input(
        stack, AF_INET6, packet + 8U, source_port, packet + 24U, target_port);
    if (flow == NULL) {
        int syn = (tcp[13] & 0x02U) != 0U;
        int ack = (tcp[13] & 0x10U) != 0U;
        if (!syn || ack) return CH_OK;
        flow = ch_ip_stack_tcp_flow_create(
            stack, AF_INET6, packet + 8U, source_port, packet + 24U,
            target_port, error);
        if (flow == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                                error->code;
    }
    memcpy(packet + 24U,
           netif_ip6_addr(&stack->interface, stack->ipv6_index), 16U);
    ch_ip_stack_write_u16(tcp + 2U, flow->internal_port);
    ch_ip_stack_ipv6_tcp_checksum(packet, length);
    return CH_OK;
}

static ch_status ch_ip_stack_prepare_tcp_input(ch_ip_stack *stack,
                                               uint8_t *packet, size_t length,
                                               ch_error *error) {
    unsigned int version = packet[0] >> 4U;
    if (version == 4U) {
        return ch_ip_stack_prepare_ipv4_tcp_input(stack, packet, length,
                                                  error);
    }
    if (version == 6U) {
        return ch_ip_stack_prepare_ipv6_tcp_input(stack, packet, length,
                                                  error);
    }
    return CH_OK;
}

static ch_status ch_ip_stack_handle_udp_payload(
    ch_ip_stack *stack, int address_family, const uint8_t *source_address,
    uint16_t source_port, const uint8_t *target_address, uint16_t target_port,
    const uint8_t *payload, size_t payload_length, int *out_handled,
    ch_error *error) {
    *out_handled = 1;
    if (target_port == 53U && stack->dns_exchange != NULL) {
        uint8_t *response = NULL;
        size_t response_length = 0U;
        ch_status status = stack->dns_exchange(
            payload, payload_length, &response, &response_length,
            stack->dns_exchange_context, error);
        if (response != NULL) {
            ch_ip_stack_dns_cache_response(stack, payload, payload_length,
                                           response, response_length);
            ch_error emit_error;
            ch_status emit_status = ch_ip_stack_emit_udp_response(
                stack, address_family, target_address, target_port,
                source_address, source_port, response, response_length,
                &emit_error);
            free(response);
            if (emit_status != CH_OK) {
                if (error != NULL) *error = emit_error;
                return emit_status;
            }
            return CH_OK;
        }
        return status;
    }
    if (stack->udp_dialer == NULL) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "UDP forwarding has no route dialer");
        return CH_ERROR_UNSUPPORTED;
    }
    ch_ip_stack_udp_flow *flow = ch_ip_stack_udp_flow_find(
        stack, address_family, source_address, source_port, target_address,
        target_port);
    if (flow == NULL) {
        flow = ch_ip_stack_udp_flow_create(
            stack, address_family, source_address, source_port,
            target_address, target_port, error);
        if (flow == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                                error->code;
    }
    char target[INET6_ADDRSTRLEN + 10U];
    if (!ch_ip_stack_format_endpoint(address_family, target_address,
                                     target_port, target)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "format UDP TUN target endpoint");
        flow->dead = 1;
        return CH_ERROR_INTERNAL;
    }
    ch_status status = stack->udp_sender(
        flow->connection, target, payload, payload_length, error);
    if (status != CH_OK) {
        flow->dead = 1;
        return status;
    }
    ch_ip_stack_observe_bytes(stack, flow->flow_id, 0U,
                              (uint64_t)payload_length);
    flow->last_activity = sys_now();
    return CH_OK;
}

static ch_status ch_ip_stack_handle_ipv4_udp(
    ch_ip_stack *stack, uint8_t *packet, size_t length, int *out_handled,
    ch_error *error) {
    *out_handled = 0;
    if (packet[9] != 17U ||
        memcmp(packet + 16U, netif_ip4_addr(&stack->interface), 4U) == 0) {
        return CH_OK;
    }
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    if (header_length < 20U || length < header_length + 8U) {
        ch_error_set(error, CH_ERROR_PARSE, "truncated IPv4 UDP packet");
        return CH_ERROR_PARSE;
    }
    uint16_t fragment = ch_ip_stack_read_u16(packet + 6U);
    if ((fragment & 0x3fffU) != 0U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "fragmented IPv4 UDP forwarding is not implemented");
        return CH_ERROR_UNSUPPORTED;
    }
    uint8_t *udp = packet + header_length;
    uint16_t udp_length = ch_ip_stack_read_u16(udp + 4U);
    if (udp_length < 8U || header_length + udp_length != length) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv4 UDP length");
        return CH_ERROR_PARSE;
    }
    uint16_t wire_checksum = ch_ip_stack_read_u16(udp + 6U);
    if (wire_checksum != 0U &&
        ch_ip_stack_udp_checksum_ipv4(packet, length) != 0U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv4 UDP checksum");
        return CH_ERROR_PARSE;
    }
    return ch_ip_stack_handle_udp_payload(
        stack, AF_INET, packet + 12U, ch_ip_stack_read_u16(udp),
        packet + 16U, ch_ip_stack_read_u16(udp + 2U), udp + 8U,
        udp_length - 8U, out_handled, error);
}

static ch_status ch_ip_stack_handle_ipv6_udp(
    ch_ip_stack *stack, uint8_t *packet, size_t length, int *out_handled,
    ch_error *error) {
    *out_handled = 0;
    uint8_t protocol = 0U;
    size_t udp_offset = 0U;
    if (!ch_ip_stack_ipv6_transport(
            packet, length, &protocol, &udp_offset)) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid IPv6 extension header chain");
        return CH_ERROR_PARSE;
    }
    if (protocol != 17U ||
        memcmp(packet + 24U,
               netif_ip6_addr(&stack->interface, stack->ipv6_index),
               16U) == 0) {
        return CH_OK;
    }
    if (length < udp_offset + 8U) {
        ch_error_set(error, CH_ERROR_PARSE, "truncated IPv6 UDP packet");
        return CH_ERROR_PARSE;
    }
    uint8_t *udp = packet + udp_offset;
    uint16_t udp_length = ch_ip_stack_read_u16(udp + 4U);
    if (udp_length < 8U || udp_offset + udp_length != length ||
        ch_ip_stack_read_u16(udp + 6U) == 0U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid IPv6 UDP length or checksum field");
        return CH_ERROR_PARSE;
    }
    if (ch_ip_stack_udp_checksum_ipv6(packet, length) != 0U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid IPv6 UDP checksum");
        return CH_ERROR_PARSE;
    }
    return ch_ip_stack_handle_udp_payload(
        stack, AF_INET6, packet + 8U, ch_ip_stack_read_u16(udp),
        packet + 24U, ch_ip_stack_read_u16(udp + 2U), udp + 8U,
        udp_length - 8U, out_handled, error);
}

static ch_status ch_ip_stack_handle_udp_input(
    ch_ip_stack *stack, uint8_t *packet, size_t length, int *out_handled,
    ch_error *error) {
    if ((packet[0] >> 4U) == 4U) {
        return ch_ip_stack_handle_ipv4_udp(stack, packet, length,
                                           out_handled, error);
    }
    return ch_ip_stack_handle_ipv6_udp(stack, packet, length, out_handled,
                                       error);
}

static err_t ch_ip_stack_emit(struct netif *interface, struct pbuf *packet) {
    ch_ip_stack *stack = interface == NULL ? NULL : interface->state;
    if (stack == NULL || packet == NULL || packet->tot_len == 0U ||
        packet->tot_len > CH_IP_STACK_MAX_PACKET) {
        return ERR_ARG;
    }
    uint8_t *bytes = malloc(packet->tot_len);
    if (bytes == NULL) return ERR_MEM;
    u16_t copied = pbuf_copy_partial(packet, bytes, packet->tot_len, 0U);
    if (copied != packet->tot_len) {
        free(bytes);
        return ERR_BUF;
    }
    ch_ip_stack_rewrite_tcp_output(stack, bytes, packet->tot_len);
    if (stack->packet_writer != NULL) {
        stack->packet_writer(bytes, packet->tot_len,
                             stack->packet_writer_context);
    }
    free(bytes);
    return ERR_OK;
}

static err_t ch_ip_stack_output_ipv4(struct netif *interface,
                                     struct pbuf *packet,
                                     const ip4_addr_t *destination) {
    (void)destination;
    return ch_ip_stack_emit(interface, packet);
}

static err_t ch_ip_stack_output_ipv6(struct netif *interface,
                                     struct pbuf *packet,
                                     const ip6_addr_t *destination) {
    (void)destination;
    return ch_ip_stack_emit(interface, packet);
}

static err_t ch_ip_stack_interface_initialize(struct netif *interface) {
    ch_ip_stack *stack = interface == NULL ? NULL : interface->state;
    if (stack == NULL) return ERR_ARG;
    interface->name[0] = 'c';
    interface->name[1] = 'h';
    interface->mtu = (u16_t)stack->mtu;
    interface->output = ch_ip_stack_output_ipv4;
    interface->output_ip6 = ch_ip_stack_output_ipv6;
    interface->flags = 0U;
    return ERR_OK;
}

ch_ip_stack *ch_ip_stack_create(const ch_ip_stack_options *options,
                                ch_error *error) {
    ch_error_clear(error);
    if (options == NULL || options->packet_writer == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "IP stack packet writer is required");
        return NULL;
    }
    unsigned int mtu = options->mtu == 0U ? CH_IP_STACK_DEFAULT_MTU :
                                            options->mtu;
    if (mtu < 1280U || mtu > UINT16_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "IP stack MTU must be between 1280 and 65535");
        return NULL;
    }
    ch_lwip_context_initialize();
    ch_lwip_context_lock();

    ch_ip_stack *stack = calloc(1U, sizeof(*stack));
    if (stack == NULL) {
        ch_lwip_context_unlock();
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate native IP stack");
        return NULL;
    }
    stack->packet_writer = options->packet_writer;
    stack->packet_writer_context = options->packet_writer_context;
    stack->tcp_dialer = options->tcp_dialer;
    stack->tcp_dialer_context = options->tcp_dialer_context;
    int has_udp_callback = options->udp_dialer != NULL ||
        options->udp_sender != NULL || options->udp_receiver != NULL ||
        options->udp_closer != NULL;
    if (has_udp_callback &&
        (options->udp_dialer == NULL || options->udp_sender == NULL ||
         options->udp_receiver == NULL || options->udp_closer == NULL)) {
        free(stack);
        ch_lwip_context_unlock();
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "IP stack UDP callbacks must be configured together");
        return NULL;
    }
    stack->udp_dialer = options->udp_dialer;
    stack->udp_dialer_context = options->udp_dialer_context;
    stack->udp_sender = options->udp_sender;
    stack->udp_receiver = options->udp_receiver;
    stack->udp_closer = options->udp_closer;
    stack->flow_bytes = options->flow_bytes;
    stack->flow_close = options->flow_close;
    stack->flow_observer_context = options->flow_observer_context;
    stack->dns_exchange = options->dns_exchange;
    stack->dns_exchange_context = options->dns_exchange_context;
    stack->next_tcp_port = CH_IP_STACK_TCP_FIRST_PORT;
    stack->ipv6_index = -1;
    stack->mtu = mtu;
    const char *ipv4_text = options->ipv4_address == NULL ||
        options->ipv4_address[0] == '\0' ? "198.18.0.1" :
                                           options->ipv4_address;
    const char *netmask_text = options->ipv4_netmask == NULL ||
        options->ipv4_netmask[0] == '\0' ? "255.255.255.252" :
                                           options->ipv4_netmask;
    const char *ipv6_text = options->ipv6_address == NULL ||
        options->ipv6_address[0] == '\0' ? "fd7a:636c:616d::1" :
                                           options->ipv6_address;
    ip4_addr_t ipv4;
    ip4_addr_t netmask;
    ip4_addr_t gateway;
    ip6_addr_t ipv6;
    ip4_addr_set_zero(&gateway);
    if (!ip4addr_aton(ipv4_text, &ipv4) ||
        !ip4addr_aton(netmask_text, &netmask) ||
        !ip6addr_aton(ipv6_text, &ipv6)) {
        free(stack);
        ch_lwip_context_unlock();
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "IP stack addresses are invalid");
        return NULL;
    }
    if (netif_add(&stack->interface, &ipv4, &netmask, &gateway, stack,
                  ch_ip_stack_interface_initialize, ip_input) == NULL) {
        free(stack);
        ch_lwip_context_unlock();
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize native IP interface");
        return NULL;
    }
    s8_t ipv6_index = -1;
    if (netif_add_ip6_address(&stack->interface, &ipv6, &ipv6_index) !=
            ERR_OK ||
        ipv6_index < 0) {
        netif_remove(&stack->interface);
        free(stack);
        ch_lwip_context_unlock();
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize native IPv6 interface");
        return NULL;
    }
    netif_ip6_addr_set_state(&stack->interface, ipv6_index,
                             IP6_ADDR_PREFERRED);
    stack->ipv6_index = ipv6_index;
    stack->previous_default = netif_default;
    netif_set_default(&stack->interface);
    netif_set_up(&stack->interface);
    netif_set_link_up(&stack->interface);
    ch_lwip_context_unlock();
    return stack;
}

void ch_ip_stack_destroy(ch_ip_stack *stack) {
    if (stack == NULL) return;
    ch_lwip_context_lock();
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        ch_ip_stack_tcp_flow_abort(flow);
    }
    ch_ip_stack_tcp_sweep(stack);
    for (ch_ip_stack_udp_flow *flow = stack->udp_flows; flow != NULL;
         flow = flow->next) {
        flow->dead = 1;
    }
    ch_ip_stack_udp_sweep(stack);
    for (ch_ip_stack_fragment_flow *flow = stack->fragment_flows;
         flow != NULL; flow = flow->next) {
        flow->dead = 1;
    }
    ch_ip_stack_fragment_sweep(stack);
    free(stack->dns_entries);
    netif_set_link_down(&stack->interface);
    netif_set_down(&stack->interface);
    if (netif_default == &stack->interface) {
        netif_set_default(stack->previous_default);
    }
    netif_remove(&stack->interface);
    free(stack);
    ch_lwip_context_unlock();
}

ch_status ch_ip_stack_inject(ch_ip_stack *stack, const uint8_t *packet,
                             size_t length, ch_error *error) {
    ch_error_clear(error);
    if (stack == NULL || packet == NULL || length < 20U ||
        length > CH_IP_STACK_MAX_PACKET) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid IP packet input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    unsigned int version = packet[0] >> 4U;
    if ((version != 4U && version != 6U) ||
        (version == 6U && length < 40U)) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "packet is not a complete IPv4 or IPv6 datagram");
        return CH_ERROR_PARSE;
    }
    ch_lwip_context_lock();
    uint8_t *transformed = NULL;
    size_t transformed_length = 0U;
    ch_status fragment_status = ch_ip_stack_prepare_input_packet(
        stack, packet, length, &transformed, &transformed_length, error);
    if (fragment_status != CH_OK) {
        ch_lwip_context_unlock();
        return fragment_status;
    }
    if (transformed == NULL) {
        ch_ip_stack_tick(stack);
        ch_lwip_context_unlock();
        return CH_OK;
    }
    ch_status prepare_status = ch_ip_stack_prepare_tcp_input(
        stack, transformed, transformed_length, error);
    if (prepare_status != CH_OK) {
        free(transformed);
        ch_lwip_context_unlock();
        return prepare_status;
    }
    int udp_handled = 0;
    ch_status udp_status = ch_ip_stack_handle_udp_input(
        stack, transformed, transformed_length, &udp_handled, error);
    if (udp_status != CH_OK || udp_handled) {
        free(transformed);
        ch_ip_stack_tick(stack);
        ch_lwip_context_unlock();
        return udp_status;
    }
    struct pbuf *buffer = pbuf_alloc(PBUF_RAW, (u16_t)transformed_length,
                                     PBUF_RAM);
    if (buffer == NULL ||
        pbuf_take(buffer, transformed, (u16_t)transformed_length) != ERR_OK) {
        free(transformed);
        if (buffer != NULL) (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate IP packet buffer");
        ch_lwip_context_unlock();
        return CH_ERROR_OUT_OF_MEMORY;
    }
    free(transformed);
    err_t result = stack->interface.input(buffer, &stack->interface);
    if (result != ERR_OK) {
        (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_PARSE,
                     "IP stack rejected packet with error %d", (int)result);
        ch_lwip_context_unlock();
        return CH_ERROR_PARSE;
    }
    ch_ip_stack_tick(stack);
    ch_lwip_context_unlock();
    return CH_OK;
}

void ch_ip_stack_tick(ch_ip_stack *stack) {
    if (stack == NULL) return;
    ch_lwip_context_lock();
    sys_check_timeouts();
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        ch_ip_stack_tcp_flow_tick(flow);
    }
    ch_ip_stack_tcp_sweep(stack);
    uint8_t *udp_buffer = malloc(stack->mtu);
    if (udp_buffer != NULL) {
        for (ch_ip_stack_udp_flow *flow = stack->udp_flows; flow != NULL;
             flow = flow->next) {
            ch_ip_stack_udp_flow_tick(flow, udp_buffer, stack->mtu);
        }
        free(udp_buffer);
    }
    ch_ip_stack_udp_sweep(stack);
    u32_t now = sys_now();
    for (ch_ip_stack_fragment_flow *flow = stack->fragment_flows;
         flow != NULL; flow = flow->next) {
        if ((u32_t)(now - flow->last_activity) >=
            CH_IP_STACK_FRAGMENT_TIMEOUT_MS) {
            flow->dead = 1;
        }
    }
    ch_ip_stack_fragment_sweep(stack);
    ch_lwip_context_unlock();
}
