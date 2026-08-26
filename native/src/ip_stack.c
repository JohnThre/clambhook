#include "clambhook/ip_stack.h"

#include <arpa/inet.h>
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
#include "lwip/tcp.h"
#include "lwip/timeouts.h"

#include "internal.h"

#define CH_IP_STACK_DEFAULT_MTU 1500U
#define CH_IP_STACK_MAX_PACKET 65535U
#define CH_IP_STACK_TCP_FIRST_PORT 20000U
#define CH_IP_STACK_TCP_LAST_PORT 59999U
#define CH_IP_STACK_TCP_QUEUE_LIMIT (1024U * 1024U)
#define CH_IP_STACK_TCP_READ_SIZE 16384U
#define CH_IP_STACK_TCP_FLOW_LIMIT 512U

typedef struct ch_ip_stack_tcp_flow ch_ip_stack_tcp_flow;

struct ch_ip_stack_tcp_flow {
    ch_ip_stack_tcp_flow *next;
    struct ch_ip_stack *stack;
    struct tcp_pcb *listener;
    struct tcp_pcb *client;
    int descriptor;
    uint8_t source_address[4];
    uint8_t target_address[4];
    uint16_t source_port;
    uint16_t target_port;
    uint16_t internal_port;
    uint8_t *pending;
    size_t pending_offset;
    size_t pending_length;
    uint8_t *remote_pending;
    size_t remote_pending_length;
    int client_eof;
    int remote_eof;
    int dead;
};

struct ch_ip_stack {
    struct netif interface;
    ch_ip_stack_packet_writer packet_writer;
    void *packet_writer_context;
    ch_ip_stack_tcp_dialer tcp_dialer;
    void *tcp_dialer_context;
    ch_ip_stack_tcp_flow *tcp_flows;
    size_t tcp_flow_count;
    uint16_t next_tcp_port;
    unsigned int mtu;
};

static pthread_once_t ch_ip_stack_lwip_once = PTHREAD_ONCE_INIT;
static pthread_mutex_t ch_ip_stack_owner_mutex = PTHREAD_MUTEX_INITIALIZER;
static int ch_ip_stack_owner_active;
static atomic_uint_fast32_t ch_ip_stack_random_fallback =
    UINT32_C(0x9e3779b9);

static void ch_ip_stack_lwip_initialize(void) {
    lwip_init();
}

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

static ch_ip_stack_tcp_flow *ch_ip_stack_tcp_flow_for_input(
    ch_ip_stack *stack, const uint8_t *source_address, uint16_t source_port,
    const uint8_t *target_address, uint16_t target_port) {
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->source_port == source_port &&
            flow->target_port == target_port &&
            memcmp(flow->source_address, source_address, 4U) == 0 &&
            memcmp(flow->target_address, target_address, 4U) == 0) {
            return flow;
        }
    }
    return NULL;
}

static ch_ip_stack_tcp_flow *ch_ip_stack_tcp_flow_for_output(
    ch_ip_stack *stack, const uint8_t *client_address, uint16_t client_port,
    uint16_t internal_port) {
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        if (!flow->dead && flow->source_port == client_port &&
            flow->internal_port == internal_port &&
            memcmp(flow->source_address, client_address, 4U) == 0) {
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
    char target_address[INET_ADDRSTRLEN];
    char source_address[INET_ADDRSTRLEN];
    if (inet_ntop(AF_INET, flow->target_address, target_address,
                  sizeof(target_address)) == NULL ||
        inet_ntop(AF_INET, flow->source_address, source_address,
                  sizeof(source_address)) == NULL) {
        flow->dead = 1;
        tcp_abort(client);
        return ERR_ABRT;
    }
    char target[INET_ADDRSTRLEN + 8U];
    char source[INET_ADDRSTRLEN + 8U];
    (void)snprintf(target, sizeof(target), "%s:%u", target_address,
                   (unsigned int)flow->target_port);
    (void)snprintf(source, sizeof(source), "%s:%u", source_address,
                   (unsigned int)flow->source_port);
    ch_error dial_error;
    int descriptor = -1;
    ch_status status = flow->stack->tcp_dialer(
        target, source, &descriptor, flow->stack->tcp_dialer_context,
        &dial_error);
    if (status != CH_OK || descriptor < 0 ||
        !ch_ip_stack_set_nonblocking(descriptor)) {
        if (descriptor >= 0) (void)close(descriptor);
        flow->dead = 1;
        tcp_abort(client);
        return ERR_ABRT;
    }
    flow->descriptor = descriptor;
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
    ch_ip_stack *stack, const uint8_t *source_address, uint16_t source_port,
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
    memcpy(flow->source_address, source_address, 4U);
    memcpy(flow->target_address, target_address, 4U);
    flow->source_port = source_port;
    flow->target_port = target_port;
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
        pcb = tcp_new_ip_type(IPADDR_TYPE_V4);
        if (pcb == NULL) {
            bind_error = ERR_MEM;
            break;
        }
        ip_addr_t local;
        ip_addr_copy_from_ip4(local, *netif_ip4_addr(&stack->interface));
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
}

static void ch_ip_stack_tcp_flow_tick(ch_ip_stack_tcp_flow *flow) {
    if (flow->dead || flow->descriptor < 0 || flow->client == NULL) return;
    while (flow->pending_offset < flow->pending_length) {
        ssize_t written = ch_ip_stack_send(
            flow->descriptor, flow->pending + flow->pending_offset,
            flow->pending_length - flow->pending_offset);
        if (written > 0) {
            flow->pending_offset += (size_t)written;
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
        ch_ip_stack_tcp_close_descriptor(flow);
        free(flow->pending);
        free(flow->remote_pending);
        free(flow);
        --stack->tcp_flow_count;
    }
}

static void ch_ip_stack_rewrite_tcp_output(ch_ip_stack *stack,
                                           uint8_t *packet, size_t length) {
    if (length < 40U || (packet[0] >> 4U) != 4U || packet[9] != 6U) return;
    size_t header_length = (size_t)(packet[0] & 0x0fU) * 4U;
    if (header_length < 20U || length < header_length + 20U) return;
    uint16_t internal_port = ch_ip_stack_read_u16(packet + header_length);
    uint16_t client_port = ch_ip_stack_read_u16(packet + header_length + 2U);
    ch_ip_stack_tcp_flow *flow = ch_ip_stack_tcp_flow_for_output(
        stack, packet + 16U, client_port, internal_port);
    if (flow == NULL) return;
    memcpy(packet + 12U, flow->target_address, 4U);
    ch_ip_stack_write_u16(packet + header_length, flow->target_port);
    ch_ip_stack_ipv4_checksums(packet, length);
}

static ch_status ch_ip_stack_prepare_tcp_input(ch_ip_stack *stack,
                                               uint8_t *packet, size_t length,
                                               ch_error *error) {
    if ((packet[0] >> 4U) != 4U || length < 20U) return CH_OK;
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
        stack, packet + 12U, source_port, packet + 16U, target_port);
    if (flow == NULL) {
        int syn = (tcp[13] & 0x02U) != 0U;
        int ack = (tcp[13] & 0x10U) != 0U;
        if (!syn || ack) return CH_OK;
        flow = ch_ip_stack_tcp_flow_create(
            stack, packet + 12U, source_port, packet + 16U, target_port,
            error);
        if (flow == NULL) return error == NULL ? CH_ERROR_INTERNAL :
                                                error->code;
    }
    memcpy(packet + 16U, netif_ip4_addr(&stack->interface), 4U);
    ch_ip_stack_write_u16(tcp + 2U, flow->internal_port);
    ch_ip_stack_ipv4_checksums(packet, length);
    return CH_OK;
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

static void ch_ip_stack_release_owner(void) {
    (void)pthread_mutex_lock(&ch_ip_stack_owner_mutex);
    ch_ip_stack_owner_active = 0;
    (void)pthread_mutex_unlock(&ch_ip_stack_owner_mutex);
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
    (void)pthread_mutex_lock(&ch_ip_stack_owner_mutex);
    if (ch_ip_stack_owner_active) {
        (void)pthread_mutex_unlock(&ch_ip_stack_owner_mutex);
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "only one native IP stack may be active");
        return NULL;
    }
    ch_ip_stack_owner_active = 1;
    (void)pthread_mutex_unlock(&ch_ip_stack_owner_mutex);
    (void)pthread_once(&ch_ip_stack_lwip_once, ch_ip_stack_lwip_initialize);

    ch_ip_stack *stack = calloc(1U, sizeof(*stack));
    if (stack == NULL) {
        ch_ip_stack_release_owner();
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate native IP stack");
        return NULL;
    }
    stack->packet_writer = options->packet_writer;
    stack->packet_writer_context = options->packet_writer_context;
    stack->tcp_dialer = options->tcp_dialer;
    stack->tcp_dialer_context = options->tcp_dialer_context;
    stack->next_tcp_port = CH_IP_STACK_TCP_FIRST_PORT;
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
        ch_ip_stack_release_owner();
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "IP stack addresses are invalid");
        return NULL;
    }
    if (netif_add(&stack->interface, &ipv4, &netmask, &gateway, stack,
                  ch_ip_stack_interface_initialize, ip_input) == NULL) {
        free(stack);
        ch_ip_stack_release_owner();
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
        ch_ip_stack_release_owner();
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize native IPv6 interface");
        return NULL;
    }
    netif_ip6_addr_set_state(&stack->interface, ipv6_index,
                             IP6_ADDR_PREFERRED);
    netif_set_default(&stack->interface);
    netif_set_up(&stack->interface);
    netif_set_link_up(&stack->interface);
    return stack;
}

void ch_ip_stack_destroy(ch_ip_stack *stack) {
    if (stack == NULL) return;
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        ch_ip_stack_tcp_flow_abort(flow);
    }
    ch_ip_stack_tcp_sweep(stack);
    netif_set_link_down(&stack->interface);
    netif_set_down(&stack->interface);
    netif_remove(&stack->interface);
    free(stack);
    ch_ip_stack_release_owner();
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
    uint8_t *transformed = malloc(length);
    if (transformed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate transformed IP packet");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(transformed, packet, length);
    ch_status prepare_status = ch_ip_stack_prepare_tcp_input(
        stack, transformed, length, error);
    if (prepare_status != CH_OK) {
        free(transformed);
        return prepare_status;
    }
    struct pbuf *buffer = pbuf_alloc(PBUF_RAW, (u16_t)length, PBUF_RAM);
    if (buffer == NULL ||
        pbuf_take(buffer, transformed, (u16_t)length) != ERR_OK) {
        free(transformed);
        if (buffer != NULL) (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate IP packet buffer");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    free(transformed);
    err_t result = stack->interface.input(buffer, &stack->interface);
    if (result != ERR_OK) {
        (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_PARSE,
                     "IP stack rejected packet with error %d", (int)result);
        return CH_ERROR_PARSE;
    }
    ch_ip_stack_tick(stack);
    return CH_OK;
}

void ch_ip_stack_tick(ch_ip_stack *stack) {
    if (stack == NULL) return;
    sys_check_timeouts();
    for (ch_ip_stack_tcp_flow *flow = stack->tcp_flows; flow != NULL;
         flow = flow->next) {
        ch_ip_stack_tcp_flow_tick(flow);
    }
    ch_ip_stack_tcp_sweep(stack);
}
