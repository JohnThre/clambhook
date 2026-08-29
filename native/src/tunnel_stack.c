// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "tunnel_stack.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "lwip/ip.h"
#include "lwip/ip_addr.h"
#include "lwip/netif.h"
#include "lwip/pbuf.h"
#include "lwip/sys.h"
#include "lwip/tcp.h"
#include "lwip/timeouts.h"
#include "lwip/udp.h"

#include "internal.h"
#include "lwip_context.h"

#define CH_TUNNEL_DEFAULT_MTU 1420U
#define CH_TUNNEL_MAX_PACKET 65535U
#define CH_TUNNEL_BUFFER_LIMIT (1024U * 1024U)
#define CH_TUNNEL_DIAL_TIMEOUT_MS 30000U
#define CH_TUNNEL_TICK_MS 10
#define CH_TUNNEL_DNS_TIMEOUT_MS 3000
#define CH_TUNNEL_DATAGRAM_LIMIT 256U
#define CH_TUNNEL_DNS_NAME_LIMIT 253U

typedef struct ch_tunnel_flow ch_tunnel_flow;
typedef struct ch_tunnel_datagram ch_tunnel_datagram;
typedef struct ch_tunnel_command ch_tunnel_command;

typedef enum ch_tunnel_command_type {
    CH_TUNNEL_COMMAND_INJECT = 1,
    CH_TUNNEL_COMMAND_DIAL = 2,
    CH_TUNNEL_COMMAND_PACKET_OPEN = 3,
    CH_TUNNEL_COMMAND_PACKET_SEND = 4,
    CH_TUNNEL_COMMAND_PACKET_CLOSE = 5
} ch_tunnel_command_type;

struct ch_tunnel_command {
    ch_tunnel_command *next;
    ch_tunnel_command_type type;
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    int complete;
    ch_status status;
    ch_error error;
    union {
        struct {
            uint8_t *bytes;
            size_t length;
        } inject;
        struct {
            ip_addr_t address;
            uint16_t port;
            int descriptor;
        } dial;
        struct {
            ch_tunnel_packet *packet;
        } packet_open;
        struct {
            ch_tunnel_packet *packet;
            ip_addr_t address;
            uint16_t port;
            const uint8_t *bytes;
            size_t length;
        } packet_send;
        struct {
            ch_tunnel_packet *packet;
        } packet_close;
    } value;
};

struct ch_tunnel_flow {
    ch_tunnel_flow *next;
    struct ch_tunnel_stack *stack;
    struct tcp_pcb *pcb;
    ch_tunnel_command *dial_command;
    int bridge_descriptor;
    uint8_t *to_local;
    size_t to_local_offset;
    size_t to_local_length;
    uint8_t *to_remote;
    size_t to_remote_offset;
    size_t to_remote_length;
    uint32_t created_at;
    int connected;
    int local_eof;
    int remote_eof;
    int tx_shutdown;
    int dead;
};

struct ch_tunnel_datagram {
    ch_tunnel_datagram *next;
    uint8_t *bytes;
    size_t length;
    char *source;
};

struct ch_tunnel_packet {
    ch_tunnel_packet *next;
    struct ch_tunnel_stack *stack;
    struct udp_pcb *ipv4;
    struct udp_pcb *ipv6;
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    ch_tunnel_datagram *head;
    ch_tunnel_datagram *tail;
    size_t queued;
    int closed;
};

struct ch_tunnel_stack {
    struct netif interface;
    ip_addr_t local_ipv4;
    ip_addr_t local_ipv6;
    int has_ipv4;
    int has_ipv6;
    s8_t ipv6_indices[LWIP_IPV6_NUM_ADDRESSES];
    size_t ipv6_count;
    unsigned int mtu;
    ch_tunnel_stack_packet_writer packet_writer;
    void *packet_writer_context;
    char **dns_servers;
    size_t dns_server_count;
    int wake_read;
    int wake_write;
    pthread_t worker;
    pthread_mutex_t queue_mutex;
    ch_tunnel_command *command_head;
    ch_tunnel_command *command_tail;
    ch_tunnel_flow *flows;
    ch_tunnel_packet *packets;
    int stopping;
};

static int ch_tunnel_set_nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL, 0);
    return flags >= 0 && fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0;
}

static void ch_tunnel_close_descriptor(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static int ch_tunnel_command_initialize(ch_tunnel_command *command,
                                        ch_tunnel_command_type type) {
    memset(command, 0, sizeof(*command));
    command->type = type;
    command->status = CH_ERROR_INTERNAL;
    if (pthread_mutex_init(&command->mutex, NULL) != 0) return 0;
    if (pthread_cond_init(&command->condition, NULL) != 0) {
        (void)pthread_mutex_destroy(&command->mutex);
        return 0;
    }
    return 1;
}

static void ch_tunnel_command_finish(ch_tunnel_command *command,
                                     ch_status status,
                                     const ch_error *error) {
    (void)pthread_mutex_lock(&command->mutex);
    command->status = status;
    if (error != NULL) command->error = *error;
    command->complete = 1;
    (void)pthread_cond_broadcast(&command->condition);
    (void)pthread_mutex_unlock(&command->mutex);
}

static ch_status ch_tunnel_command_wait(ch_tunnel_command *command,
                                        ch_error *error) {
    (void)pthread_mutex_lock(&command->mutex);
    while (!command->complete) {
        (void)pthread_cond_wait(&command->condition, &command->mutex);
    }
    ch_status status = command->status;
    if (error != NULL) *error = command->error;
    (void)pthread_mutex_unlock(&command->mutex);
    (void)pthread_cond_destroy(&command->condition);
    (void)pthread_mutex_destroy(&command->mutex);
    return status;
}

static ch_status ch_tunnel_submit(ch_tunnel_stack *stack,
                                  ch_tunnel_command *command,
                                  ch_error *error) {
    (void)pthread_mutex_lock(&stack->queue_mutex);
    if (stack->stopping) {
        (void)pthread_mutex_unlock(&stack->queue_mutex);
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "tunnel stack is stopping");
        ch_tunnel_command_finish(command, CH_ERROR_INVALID_STATE, error);
        return ch_tunnel_command_wait(command, error);
    }
    if (stack->command_tail == NULL) {
        stack->command_head = command;
    } else {
        stack->command_tail->next = command;
    }
    stack->command_tail = command;
    uint8_t wake = 1U;
    (void)write(stack->wake_write, &wake, sizeof(wake));
    (void)pthread_mutex_unlock(&stack->queue_mutex);
    return ch_tunnel_command_wait(command, error);
}

static ch_tunnel_command *ch_tunnel_take_commands(ch_tunnel_stack *stack) {
    (void)pthread_mutex_lock(&stack->queue_mutex);
    ch_tunnel_command *commands = stack->command_head;
    stack->command_head = NULL;
    stack->command_tail = NULL;
    (void)pthread_mutex_unlock(&stack->queue_mutex);
    return commands;
}

static int ch_tunnel_parse_endpoint(const char *target,
                                    ip_addr_t *out_address,
                                    uint16_t *out_port) {
    if (target == NULL || out_address == NULL || out_port == NULL) return 0;
    const char *host_start = target;
    const char *host_end = NULL;
    const char *port_start = NULL;
    if (target[0] == '[') {
        host_start = target + 1U;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            return 0;
        }
        port_start = host_end + 2U;
    } else {
        host_end = strrchr(target, ':');
        if (host_end == NULL || host_end == target || host_end[1] == '\0' ||
            strchr(target, ':') != host_end) {
            return 0;
        }
        port_start = host_end + 1U;
    }
    size_t host_length = (size_t)(host_end - host_start);
    if (host_length == 0U || host_length >= INET6_ADDRSTRLEN) return 0;
    char host[INET6_ADDRSTRLEN];
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    char *end = NULL;
    errno = 0;
    unsigned long port = strtoul(port_start, &end, 10);
    if (errno != 0 || end == port_start || *end != '\0' || port == 0UL ||
        port > UINT16_MAX || !ipaddr_aton(host, out_address)) {
        return 0;
    }
    *out_port = (uint16_t)port;
    return 1;
}

static int ch_tunnel_format_endpoint(const ip_addr_t *address, uint16_t port,
                                     char **out_source) {
    char host[IPADDR_STRLEN_MAX];
    if (ipaddr_ntoa_r(address, host, sizeof(host)) == NULL) return 0;
    int ipv6 = IP_IS_V6(address);
    size_t capacity = strlen(host) + 10U;
    char *source = malloc(capacity);
    if (source == NULL) return 0;
    int length = snprintf(source, capacity, ipv6 ? "[%s]:%u" : "%s:%u",
                          host, (unsigned int)port);
    if (length <= 0 || (size_t)length >= capacity) {
        free(source);
        return 0;
    }
    *out_source = source;
    return 1;
}

static int ch_tunnel_buffer_append(uint8_t **buffer, size_t *offset,
                                   size_t *length, const uint8_t *bytes,
                                   size_t amount) {
    if (amount == 0U) return 1;
    if (*length > CH_TUNNEL_BUFFER_LIMIT - amount) return 0;
    if (*offset > 0U && *length > 0U) {
        memmove(*buffer, *buffer + *offset, *length);
        *offset = 0U;
    }
    uint8_t *next = realloc(*buffer, *length + amount);
    if (next == NULL) return 0;
    memcpy(next + *length, bytes, amount);
    *buffer = next;
    *length += amount;
    return 1;
}

static err_t ch_tunnel_emit(struct netif *interface, struct pbuf *packet) {
    ch_tunnel_stack *stack = interface == NULL ? NULL : interface->state;
    if (stack == NULL || packet == NULL || packet->tot_len == 0U) {
        return ERR_ARG;
    }
    uint8_t *bytes = malloc(packet->tot_len);
    if (bytes == NULL) return ERR_MEM;
    if (pbuf_copy_partial(packet, bytes, packet->tot_len, 0U) !=
        packet->tot_len) {
        free(bytes);
        return ERR_BUF;
    }
    stack->packet_writer(bytes, packet->tot_len,
                         stack->packet_writer_context);
    free(bytes);
    return ERR_OK;
}

static err_t ch_tunnel_output_ipv4(struct netif *interface,
                                   struct pbuf *packet,
                                   const ip4_addr_t *destination) {
    (void)destination;
    return ch_tunnel_emit(interface, packet);
}

static err_t ch_tunnel_output_ipv6(struct netif *interface,
                                   struct pbuf *packet,
                                   const ip6_addr_t *destination) {
    (void)destination;
    return ch_tunnel_emit(interface, packet);
}

static err_t ch_tunnel_interface_initialize(struct netif *interface) {
    ch_tunnel_stack *stack = interface == NULL ? NULL : interface->state;
    if (stack == NULL) return ERR_ARG;
    interface->name[0] = 'v';
    interface->name[1] = 'p';
    interface->mtu = (u16_t)stack->mtu;
    interface->output = ch_tunnel_output_ipv4;
    interface->output_ip6 = ch_tunnel_output_ipv6;
    interface->flags = 0U;
    return ERR_OK;
}

static void ch_tunnel_flow_signal_error(ch_tunnel_flow *flow,
                                        ch_status status,
                                        const char *message) {
    if (flow->dial_command == NULL) return;
    ch_error error;
    ch_error_clear(&error);
    ch_error_set(&error, status, "%s", message);
    ch_tunnel_command_finish(flow->dial_command, status, &error);
    flow->dial_command = NULL;
}

static void ch_tunnel_flow_release_pcb(ch_tunnel_flow *flow, int aborting) {
    if (flow->pcb == NULL) return;
    tcp_arg(flow->pcb, NULL);
    tcp_recv(flow->pcb, NULL);
    tcp_err(flow->pcb, NULL);
    if (aborting || tcp_close(flow->pcb) != ERR_OK) {
        tcp_abort(flow->pcb);
    }
    flow->pcb = NULL;
}

static void ch_tunnel_flow_destroy(ch_tunnel_flow *flow) {
    if (flow == NULL) return;
    ch_tunnel_flow_release_pcb(flow, 1);
    if (flow->dial_command != NULL) {
        ch_tunnel_flow_signal_error(flow, CH_ERROR_IO,
                                    "tunnel TCP connection closed");
    }
    ch_tunnel_close_descriptor(&flow->bridge_descriptor);
    free(flow->to_local);
    free(flow->to_remote);
    free(flow);
}

static err_t ch_tunnel_tcp_connected(void *context, struct tcp_pcb *pcb,
                                     err_t result) {
    ch_tunnel_flow *flow = context;
    if (flow == NULL || flow->dead || result != ERR_OK) {
        if (flow != NULL) {
            ch_tunnel_flow_signal_error(flow, CH_ERROR_IO,
                                        "tunnel TCP connect failed");
            flow->dead = 1;
        }
        return result == ERR_OK ? ERR_ABRT : result;
    }
    int descriptors[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, descriptors) != 0 ||
        !ch_tunnel_set_nonblocking(descriptors[1])) {
        ch_tunnel_close_descriptor(&descriptors[0]);
        ch_tunnel_close_descriptor(&descriptors[1]);
        ch_tunnel_flow_signal_error(flow, CH_ERROR_IO,
                                    "create tunnel TCP bridge failed");
        flow->dead = 1;
        return ERR_ABRT;
    }
    flow->bridge_descriptor = descriptors[1];
    flow->connected = 1;
    flow->dial_command->value.dial.descriptor = descriptors[0];
    ch_tunnel_command_finish(flow->dial_command, CH_OK, NULL);
    flow->dial_command = NULL;
    tcp_nagle_disable(pcb);
    return ERR_OK;
}

static err_t ch_tunnel_tcp_receive(void *context, struct tcp_pcb *pcb,
                                   struct pbuf *packet, err_t result) {
    (void)pcb;
    ch_tunnel_flow *flow = context;
    if (flow == NULL || flow->dead) {
        if (packet != NULL) (void)pbuf_free(packet);
        return ERR_ABRT;
    }
    if (result != ERR_OK) {
        if (packet != NULL) (void)pbuf_free(packet);
        flow->dead = 1;
        return result;
    }
    if (packet == NULL) {
        flow->remote_eof = 1;
        return ERR_OK;
    }
    if (flow->to_local_length > CH_TUNNEL_BUFFER_LIMIT - packet->tot_len) {
        return ERR_MEM;
    }
    uint8_t *bytes = malloc(packet->tot_len);
    if (bytes == NULL) return ERR_MEM;
    if (pbuf_copy_partial(packet, bytes, packet->tot_len, 0U) !=
        packet->tot_len ||
        !ch_tunnel_buffer_append(&flow->to_local,
                                 &flow->to_local_offset,
                                 &flow->to_local_length,
                                 bytes, packet->tot_len)) {
        free(bytes);
        return ERR_MEM;
    }
    free(bytes);
    (void)pbuf_free(packet);
    return ERR_OK;
}

static void ch_tunnel_tcp_error(void *context, err_t result) {
    (void)result;
    ch_tunnel_flow *flow = context;
    if (flow == NULL) return;
    flow->pcb = NULL;
    ch_tunnel_flow_signal_error(flow, CH_ERROR_IO,
                                "tunnel TCP connection failed");
    flow->dead = 1;
}

static void ch_tunnel_flow_tick(ch_tunnel_flow *flow) {
    if (flow->dead) return;
    if (!flow->connected) {
        if ((u32_t)(sys_now() - flow->created_at) >=
            CH_TUNNEL_DIAL_TIMEOUT_MS) {
            ch_tunnel_flow_signal_error(flow, CH_ERROR_IO,
                                        "tunnel TCP connect timed out");
            flow->dead = 1;
        }
        return;
    }
    if (flow->to_local_length > 0U) {
        ssize_t written = send(flow->bridge_descriptor,
                               flow->to_local + flow->to_local_offset,
                               flow->to_local_length, 0);
        if (written > 0) {
            size_t amount = (size_t)written;
            flow->to_local_offset += amount;
            flow->to_local_length -= amount;
            if (flow->pcb != NULL) tcp_recved(flow->pcb, (u16_t)amount);
            if (flow->to_local_length == 0U) {
                free(flow->to_local);
                flow->to_local = NULL;
                flow->to_local_offset = 0U;
            }
        } else if (written < 0 && errno != EINTR && errno != EAGAIN &&
                   errno != EWOULDBLOCK) {
            flow->local_eof = 1;
            flow->dead = 1;
        }
    }
    if (!flow->local_eof && flow->to_remote_length == 0U) {
        uint8_t bytes[16384];
        ssize_t received = recv(flow->bridge_descriptor, bytes,
                                sizeof(bytes), 0);
        if (received > 0) {
            if (!ch_tunnel_buffer_append(&flow->to_remote,
                                         &flow->to_remote_offset,
                                         &flow->to_remote_length,
                                         bytes, (size_t)received)) {
                flow->dead = 1;
            }
        } else if (received == 0) {
            flow->local_eof = 1;
        } else if (errno != EINTR && errno != EAGAIN &&
                   errno != EWOULDBLOCK) {
            flow->local_eof = 1;
            flow->dead = 1;
        }
    }
    if (flow->pcb != NULL && flow->to_remote_length > 0U) {
        u16_t available = tcp_sndbuf(flow->pcb);
        size_t amount = flow->to_remote_length;
        if (amount > available) amount = available;
        if (amount > UINT16_MAX) amount = UINT16_MAX;
        if (amount > 0U && tcp_write(
                flow->pcb, flow->to_remote + flow->to_remote_offset,
                (u16_t)amount, TCP_WRITE_FLAG_COPY) == ERR_OK) {
            flow->to_remote_offset += amount;
            flow->to_remote_length -= amount;
            (void)tcp_output(flow->pcb);
            if (flow->to_remote_length == 0U) {
                free(flow->to_remote);
                flow->to_remote = NULL;
                flow->to_remote_offset = 0U;
            }
        }
    }
    if (flow->pcb != NULL && flow->local_eof &&
        flow->to_remote_length == 0U && !flow->tx_shutdown) {
        if (tcp_shutdown(flow->pcb, 0, 1) == ERR_OK) {
            flow->tx_shutdown = 1;
        }
    }
    if (flow->remote_eof && flow->to_local_length == 0U) {
        (void)shutdown(flow->bridge_descriptor, SHUT_WR);
    }
    if (flow->local_eof && flow->remote_eof &&
        flow->to_local_length == 0U && flow->to_remote_length == 0U) {
        flow->dead = 1;
    }
}

static void ch_tunnel_flow_sweep(ch_tunnel_stack *stack) {
    ch_tunnel_flow **cursor = &stack->flows;
    while (*cursor != NULL) {
        ch_tunnel_flow *flow = *cursor;
        if (!flow->dead) {
            cursor = &flow->next;
            continue;
        }
        *cursor = flow->next;
        ch_tunnel_flow_destroy(flow);
    }
}

static void ch_tunnel_packet_queue_clear(ch_tunnel_packet *packet) {
    ch_tunnel_datagram *item = packet->head;
    while (item != NULL) {
        ch_tunnel_datagram *next = item->next;
        free(item->bytes);
        free(item->source);
        free(item);
        item = next;
    }
    packet->head = NULL;
    packet->tail = NULL;
    packet->queued = 0U;
}

static void ch_tunnel_udp_receive(void *context, struct udp_pcb *pcb,
                                  struct pbuf *value,
                                  const ip_addr_t *address, u16_t port) {
    (void)pcb;
    ch_tunnel_packet *packet = context;
    if (packet == NULL || value == NULL) {
        if (value != NULL) (void)pbuf_free(value);
        return;
    }
    ch_tunnel_datagram *item = calloc(1U, sizeof(*item));
    if (item != NULL) {
        item->bytes = malloc(value->tot_len);
        if (item->bytes == NULL ||
            pbuf_copy_partial(value, item->bytes, value->tot_len, 0U) !=
                value->tot_len ||
            !ch_tunnel_format_endpoint(address, port, &item->source)) {
            free(item->bytes);
            free(item->source);
            free(item);
            item = NULL;
        } else {
            item->length = value->tot_len;
        }
    }
    (void)pbuf_free(value);
    if (item == NULL) return;
    (void)pthread_mutex_lock(&packet->mutex);
    if (packet->closed || packet->queued >= CH_TUNNEL_DATAGRAM_LIMIT) {
        (void)pthread_mutex_unlock(&packet->mutex);
        free(item->bytes);
        free(item->source);
        free(item);
        return;
    }
    if (packet->tail == NULL) packet->head = item;
    else packet->tail->next = item;
    packet->tail = item;
    ++packet->queued;
    (void)pthread_cond_broadcast(&packet->condition);
    (void)pthread_mutex_unlock(&packet->mutex);
}

static struct udp_pcb *ch_tunnel_packet_pcb(ch_tunnel_stack *stack,
                                             ch_tunnel_packet *packet,
                                             const ip_addr_t *target,
                                             ch_error *error) {
    struct udp_pcb **slot = IP_IS_V6(target) ? &packet->ipv6 : &packet->ipv4;
    if (*slot != NULL) return *slot;
    if ((IP_IS_V6(target) && !stack->has_ipv6) ||
        (!IP_IS_V6(target) && !stack->has_ipv4)) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "tunnel has no matching local address family");
        return NULL;
    }
    u8_t type = IP_IS_V6(target) ? IPADDR_TYPE_V6 : IPADDR_TYPE_V4;
    struct udp_pcb *pcb = udp_new_ip_type(type);
    if (pcb == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel UDP socket");
        return NULL;
    }
    const ip_addr_t *local = IP_IS_V6(target) ? &stack->local_ipv6 :
                                               &stack->local_ipv4;
    if (udp_bind(pcb, local, 0U) != ERR_OK) {
        udp_remove(pcb);
        ch_error_set(error, CH_ERROR_IO, "bind tunnel UDP socket");
        return NULL;
    }
    udp_bind_netif(pcb, &stack->interface);
    udp_recv(pcb, ch_tunnel_udp_receive, packet);
    *slot = pcb;
    return pcb;
}

static void ch_tunnel_packet_detach(ch_tunnel_stack *stack,
                                    ch_tunnel_packet *packet) {
    ch_tunnel_packet **cursor = &stack->packets;
    while (*cursor != NULL) {
        if (*cursor == packet) {
            *cursor = packet->next;
            break;
        }
        cursor = &(*cursor)->next;
    }
    if (packet->ipv4 != NULL) udp_remove(packet->ipv4);
    if (packet->ipv6 != NULL) udp_remove(packet->ipv6);
    packet->ipv4 = NULL;
    packet->ipv6 = NULL;
    (void)pthread_mutex_lock(&packet->mutex);
    packet->stack = NULL;
    packet->closed = 1;
    (void)pthread_cond_broadcast(&packet->condition);
    (void)pthread_mutex_unlock(&packet->mutex);
}

static void ch_tunnel_process_inject(ch_tunnel_stack *stack,
                                     ch_tunnel_command *command) {
    struct pbuf *buffer = pbuf_alloc(PBUF_RAW,
        (u16_t)command->value.inject.length, PBUF_RAM);
    ch_error error;
    ch_error_clear(&error);
    if (buffer == NULL || pbuf_take(
            buffer, command->value.inject.bytes,
            (u16_t)command->value.inject.length) != ERR_OK) {
        if (buffer != NULL) (void)pbuf_free(buffer);
        ch_error_set(&error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel input packet");
        ch_tunnel_command_finish(command, CH_ERROR_OUT_OF_MEMORY, &error);
        return;
    }
    err_t result = stack->interface.input(buffer, &stack->interface);
    if (result != ERR_OK) {
        (void)pbuf_free(buffer);
        ch_error_set(&error, CH_ERROR_PARSE,
                     "tunnel stack rejected packet with error %d",
                     (int)result);
        ch_tunnel_command_finish(command, CH_ERROR_PARSE, &error);
        return;
    }
    ch_tunnel_command_finish(command, CH_OK, NULL);
}

static void ch_tunnel_process_dial(ch_tunnel_stack *stack,
                                   ch_tunnel_command *command) {
    const ip_addr_t *target = &command->value.dial.address;
    if ((IP_IS_V6(target) && !stack->has_ipv6) ||
        (!IP_IS_V6(target) && !stack->has_ipv4)) {
        ch_error error;
        ch_error_clear(&error);
        ch_error_set(&error, CH_ERROR_UNSUPPORTED,
                     "tunnel has no matching local address family");
        ch_tunnel_command_finish(command, CH_ERROR_UNSUPPORTED, &error);
        return;
    }
    ch_tunnel_flow *flow = calloc(1U, sizeof(*flow));
    if (flow == NULL) {
        ch_error error;
        ch_error_clear(&error);
        ch_error_set(&error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel TCP flow");
        ch_tunnel_command_finish(command, CH_ERROR_OUT_OF_MEMORY, &error);
        return;
    }
    flow->stack = stack;
    flow->bridge_descriptor = -1;
    flow->dial_command = command;
    flow->created_at = sys_now();
    flow->pcb = tcp_new_ip_type(IP_IS_V6(target) ? IPADDR_TYPE_V6 :
                                                    IPADDR_TYPE_V4);
    const ip_addr_t *local = IP_IS_V6(target) ? &stack->local_ipv6 :
                                               &stack->local_ipv4;
    err_t result = flow->pcb == NULL ? ERR_MEM :
        tcp_bind(flow->pcb, local, 0U);
    if (result == ERR_OK) {
        tcp_bind_netif(flow->pcb, &stack->interface);
        tcp_arg(flow->pcb, flow);
        tcp_recv(flow->pcb, ch_tunnel_tcp_receive);
        tcp_err(flow->pcb, ch_tunnel_tcp_error);
        result = tcp_connect(flow->pcb, target, command->value.dial.port,
                             ch_tunnel_tcp_connected);
    }
    if (result != ERR_OK) {
        ch_error error;
        ch_error_clear(&error);
        ch_error_set(&error, result == ERR_MEM ? CH_ERROR_OUT_OF_MEMORY :
                                                CH_ERROR_IO,
                     "start tunnel TCP connection: lwIP error %d",
                     (int)result);
        ch_tunnel_command_finish(command, error.code, &error);
        flow->dial_command = NULL;
        ch_tunnel_flow_destroy(flow);
        return;
    }
    flow->next = stack->flows;
    stack->flows = flow;
}

static void ch_tunnel_process_packet_open(ch_tunnel_stack *stack,
                                          ch_tunnel_command *command) {
    ch_tunnel_packet *packet = calloc(1U, sizeof(*packet));
    if (packet == NULL || pthread_mutex_init(&packet->mutex, NULL) != 0 ||
        pthread_cond_init(&packet->condition, NULL) != 0) {
        free(packet);
        ch_error error;
        ch_error_clear(&error);
        ch_error_set(&error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel datagram socket");
        ch_tunnel_command_finish(command, CH_ERROR_OUT_OF_MEMORY, &error);
        return;
    }
    packet->stack = stack;
    packet->next = stack->packets;
    stack->packets = packet;
    command->value.packet_open.packet = packet;
    ch_tunnel_command_finish(command, CH_OK, NULL);
}

static void ch_tunnel_process_packet_send(ch_tunnel_stack *stack,
                                          ch_tunnel_command *command) {
    ch_error error;
    ch_error_clear(&error);
    ch_tunnel_packet *packet = command->value.packet_send.packet;
    struct udp_pcb *pcb = ch_tunnel_packet_pcb(
        stack, packet, &command->value.packet_send.address, &error);
    if (pcb == NULL) {
        ch_tunnel_command_finish(command, error.code, &error);
        return;
    }
    struct pbuf *buffer = pbuf_alloc(PBUF_TRANSPORT,
        (u16_t)command->value.packet_send.length, PBUF_RAM);
    if (buffer == NULL || (command->value.packet_send.length > 0U &&
        pbuf_take(buffer, command->value.packet_send.bytes,
                  (u16_t)command->value.packet_send.length) != ERR_OK)) {
        if (buffer != NULL) (void)pbuf_free(buffer);
        ch_error_set(&error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel UDP packet");
        ch_tunnel_command_finish(command, CH_ERROR_OUT_OF_MEMORY, &error);
        return;
    }
    err_t result = udp_sendto(pcb, buffer,
                              &command->value.packet_send.address,
                              command->value.packet_send.port);
    (void)pbuf_free(buffer);
    if (result != ERR_OK) {
        ch_error_set(&error, CH_ERROR_IO,
                     "send tunnel UDP packet: lwIP error %d", (int)result);
        ch_tunnel_command_finish(command, CH_ERROR_IO, &error);
        return;
    }
    ch_tunnel_command_finish(command, CH_OK, NULL);
}

static void ch_tunnel_process_packet_close(ch_tunnel_stack *stack,
                                           ch_tunnel_command *command) {
    ch_tunnel_packet_detach(stack, command->value.packet_close.packet);
    ch_tunnel_command_finish(command, CH_OK, NULL);
}

static void ch_tunnel_process_commands(ch_tunnel_stack *stack) {
    ch_tunnel_command *command = ch_tunnel_take_commands(stack);
    while (command != NULL) {
        ch_tunnel_command *next = command->next;
        command->next = NULL;
        switch (command->type) {
            case CH_TUNNEL_COMMAND_INJECT:
                ch_tunnel_process_inject(stack, command);
                break;
            case CH_TUNNEL_COMMAND_DIAL:
                ch_tunnel_process_dial(stack, command);
                break;
            case CH_TUNNEL_COMMAND_PACKET_OPEN:
                ch_tunnel_process_packet_open(stack, command);
                break;
            case CH_TUNNEL_COMMAND_PACKET_SEND:
                ch_tunnel_process_packet_send(stack, command);
                break;
            case CH_TUNNEL_COMMAND_PACKET_CLOSE:
                ch_tunnel_process_packet_close(stack, command);
                break;
        }
        command = next;
    }
}

static void ch_tunnel_drain_wake(ch_tunnel_stack *stack) {
    uint8_t bytes[64];
    while (read(stack->wake_read, bytes, sizeof(bytes)) > 0) {
    }
}

static void ch_tunnel_worker_cleanup(ch_tunnel_stack *stack) {
    ch_tunnel_command *command = ch_tunnel_take_commands(stack);
    while (command != NULL) {
        ch_tunnel_command *next = command->next;
        ch_error error;
        ch_error_clear(&error);
        ch_error_set(&error, CH_ERROR_INVALID_STATE,
                     "tunnel stack stopped");
        ch_tunnel_command_finish(command, CH_ERROR_INVALID_STATE, &error);
        command = next;
    }
    while (stack->flows != NULL) {
        ch_tunnel_flow *next = stack->flows->next;
        ch_tunnel_flow_destroy(stack->flows);
        stack->flows = next;
    }
    while (stack->packets != NULL) {
        ch_tunnel_packet *next = stack->packets->next;
        ch_tunnel_packet_detach(stack, stack->packets);
        stack->packets = next;
    }
    netif_set_link_down(&stack->interface);
    netif_set_down(&stack->interface);
    netif_remove(&stack->interface);
}

static void *ch_tunnel_worker_main(void *opaque) {
    ch_tunnel_stack *stack = opaque;
    for (;;) {
        struct pollfd wake = {.fd = stack->wake_read, .events = POLLIN};
        (void)poll(&wake, 1U, CH_TUNNEL_TICK_MS);
        ch_lwip_context_lock();
        if ((wake.revents & POLLIN) != 0) ch_tunnel_drain_wake(stack);
        ch_tunnel_process_commands(stack);
        for (ch_tunnel_flow *flow = stack->flows; flow != NULL;
             flow = flow->next) {
            ch_tunnel_flow_tick(flow);
        }
        ch_tunnel_flow_sweep(stack);
        sys_check_timeouts();
        (void)pthread_mutex_lock(&stack->queue_mutex);
        int stopping = stack->stopping;
        (void)pthread_mutex_unlock(&stack->queue_mutex);
        if (stopping) {
            ch_tunnel_worker_cleanup(stack);
            ch_lwip_context_unlock();
            break;
        }
        ch_lwip_context_unlock();
    }
    return NULL;
}

static int ch_tunnel_split_target(const char *target, char **out_host,
                                  uint16_t *out_port) {
    *out_host = NULL;
    if (target == NULL || out_port == NULL) return 0;
    const char *start = target;
    const char *end = NULL;
    const char *port_start = NULL;
    if (target[0] == '[') {
        start = target + 1U;
        end = strchr(start, ']');
        if (end == NULL || end[1] != ':' || end[2] == '\0') return 0;
        port_start = end + 2U;
    } else {
        end = strrchr(target, ':');
        if (end == NULL || end == target || end[1] == '\0' ||
            strchr(target, ':') != end) return 0;
        port_start = end + 1U;
    }
    size_t length = (size_t)(end - start);
    if (length == 0U || length > CH_TUNNEL_DNS_NAME_LIMIT) return 0;
    char *host = malloc(length + 1U);
    if (host == NULL) return 0;
    memcpy(host, start, length);
    host[length] = '\0';
    char *port_end = NULL;
    errno = 0;
    unsigned long port = strtoul(port_start, &port_end, 10);
    if (errno != 0 || port_end == port_start || *port_end != '\0' ||
        port == 0UL || port > UINT16_MAX) {
        free(host);
        return 0;
    }
    *out_host = host;
    *out_port = (uint16_t)port;
    return 1;
}

static int ch_tunnel_dns_skip_name(const uint8_t *message, size_t length,
                                   size_t *offset) {
    size_t cursor = *offset;
    unsigned int labels = 0U;
    while (cursor < length && labels++ < 128U) {
        uint8_t size = message[cursor++];
        if ((size & 0xc0U) == 0xc0U) {
            if (cursor >= length) return 0;
            *offset = cursor + 1U;
            return 1;
        }
        if ((size & 0xc0U) != 0U || size > 63U ||
            cursor + size > length) return 0;
        if (size == 0U) {
            *offset = cursor;
            return 1;
        }
        cursor += size;
    }
    return 0;
}

static int ch_tunnel_dns_query(const char *host, uint16_t type,
                               uint16_t identifier,
                               uint8_t **out_query, size_t *out_length) {
    size_t host_length = strlen(host);
    if (host_length == 0U || host_length > CH_TUNNEL_DNS_NAME_LIMIT) return 0;
    uint8_t *query = calloc(1U, 12U + host_length + 2U + 4U);
    if (query == NULL) return 0;
    query[0] = (uint8_t)(identifier >> 8U);
    query[1] = (uint8_t)identifier;
    query[2] = 0x01U;
    query[5] = 0x01U;
    size_t offset = 12U;
    const char *label = host;
    for (;;) {
        const char *dot = strchr(label, '.');
        size_t label_length = dot == NULL ? strlen(label) :
                                             (size_t)(dot - label);
        if (label_length == 0U || label_length > 63U) {
            free(query);
            return 0;
        }
        query[offset++] = (uint8_t)label_length;
        memcpy(query + offset, label, label_length);
        offset += label_length;
        if (dot == NULL) break;
        label = dot + 1U;
    }
    query[offset++] = 0U;
    query[offset++] = (uint8_t)(type >> 8U);
    query[offset++] = (uint8_t)type;
    query[offset++] = 0U;
    query[offset++] = 1U;
    *out_query = query;
    *out_length = offset;
    return 1;
}

static int ch_tunnel_dns_answer(const uint8_t *message, size_t length,
                                uint16_t identifier, uint16_t wanted_type,
                                ip_addr_t *out_address) {
    if (length < 12U || message[0] != (uint8_t)(identifier >> 8U) ||
        message[1] != (uint8_t)identifier || (message[2] & 0x80U) == 0U ||
        (message[3] & 0x0fU) != 0U) return 0;
    uint16_t questions = (uint16_t)(((uint16_t)message[4] << 8U) |
                                    message[5]);
    uint16_t answers = (uint16_t)(((uint16_t)message[6] << 8U) |
                                  message[7]);
    size_t offset = 12U;
    for (uint16_t index = 0U; index < questions; ++index) {
        if (!ch_tunnel_dns_skip_name(message, length, &offset) ||
            offset + 4U > length) return 0;
        offset += 4U;
    }
    for (uint16_t index = 0U; index < answers; ++index) {
        if (!ch_tunnel_dns_skip_name(message, length, &offset) ||
            offset + 10U > length) return 0;
        uint16_t type = (uint16_t)(((uint16_t)message[offset] << 8U) |
                                   message[offset + 1U]);
        uint16_t record_class = (uint16_t)(
            ((uint16_t)message[offset + 2U] << 8U) | message[offset + 3U]);
        uint16_t data_length = (uint16_t)(
            ((uint16_t)message[offset + 8U] << 8U) | message[offset + 9U]);
        offset += 10U;
        if (offset + data_length > length) return 0;
        if (record_class == 1U && type == wanted_type &&
            ((type == 1U && data_length == 4U) ||
             (type == 28U && data_length == 16U))) {
            char text[INET6_ADDRSTRLEN];
            int family = type == 1U ? AF_INET : AF_INET6;
            if (inet_ntop(family, message + offset, text, sizeof(text)) !=
                    NULL && ipaddr_aton(text, out_address)) {
                return 1;
            }
        }
        offset += data_length;
    }
    return 0;
}

static ch_status ch_tunnel_packet_send_numeric(ch_tunnel_packet *packet,
                                                const ip_addr_t *address,
                                                uint16_t port,
                                                const uint8_t *payload,
                                                size_t payload_length,
                                                ch_error *error) {
    if (payload_length > UINT16_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel UDP payload is too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    (void)pthread_mutex_lock(&packet->mutex);
    ch_tunnel_stack *stack = packet->stack;
    int closed = packet->closed;
    (void)pthread_mutex_unlock(&packet->mutex);
    if (stack == NULL || closed) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "tunnel datagram socket is closed");
        return CH_ERROR_INVALID_STATE;
    }
    ch_tunnel_command command;
    if (!ch_tunnel_command_initialize(&command,
                                      CH_TUNNEL_COMMAND_PACKET_SEND)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize tunnel UDP command");
        return CH_ERROR_INTERNAL;
    }
    command.value.packet_send.packet = packet;
    command.value.packet_send.address = *address;
    command.value.packet_send.port = port;
    command.value.packet_send.bytes = payload;
    command.value.packet_send.length = payload_length;
    return ch_tunnel_submit(stack, &command, error);
}

static ch_status ch_tunnel_resolve_host(ch_tunnel_stack *stack,
                                        const char *host,
                                        ip_addr_t *out_address,
                                        ch_error *error) {
    if (ipaddr_aton(host, out_address)) return CH_OK;
    if (stack->dns_server_count == 0U) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "tunnel DNS is not configured for %s", host);
        return CH_ERROR_NOT_FOUND;
    }
    const uint16_t types[2] = {1U, 28U};
    for (size_t dns_index = 0U; dns_index < stack->dns_server_count;
         ++dns_index) {
        ip_addr_t dns_address;
        if (!ipaddr_aton(stack->dns_servers[dns_index], &dns_address)) {
            continue;
        }
        for (size_t type_index = 0U; type_index < 2U; ++type_index) {
            if ((types[type_index] == 1U && !stack->has_ipv4) ||
                (types[type_index] == 28U && !stack->has_ipv6)) continue;
            ch_tunnel_packet *packet = NULL;
            ch_error attempt;
            ch_error_clear(&attempt);
            if (ch_tunnel_stack_open_packet(stack, &packet, &attempt) !=
                CH_OK) continue;
            uint16_t identifier = (uint16_t)(sys_now() ^
                (uint32_t)(dns_index * 257U + type_index * 17U));
            uint8_t *query = NULL;
            size_t query_length = 0U;
            int built = ch_tunnel_dns_query(host, types[type_index],
                                            identifier, &query,
                                            &query_length);
            ch_status sent = built ? ch_tunnel_packet_send_numeric(
                packet, &dns_address, 53U, query, query_length, &attempt) :
                CH_ERROR_INVALID_ARGUMENT;
            free(query);
            uint8_t response[4096];
            size_t response_length = 0U;
            char *source = NULL;
            ch_status received = sent == CH_OK ? ch_tunnel_packet_receive(
                packet, response, sizeof(response), &response_length,
                &source, CH_TUNNEL_DNS_TIMEOUT_MS, &attempt) : sent;
            free(source);
            ch_tunnel_packet_close(packet);
            if (received == CH_OK && ch_tunnel_dns_answer(
                    response, response_length, identifier,
                    types[type_index], out_address)) {
                return CH_OK;
            }
        }
    }
    ch_error_set(error, CH_ERROR_NOT_FOUND,
                 "tunnel DNS could not resolve %s", host);
    return CH_ERROR_NOT_FOUND;
}

static ch_status ch_tunnel_resolve_target(ch_tunnel_stack *stack,
                                          const char *target,
                                          ip_addr_t *out_address,
                                          uint16_t *out_port,
                                          ch_error *error) {
    if (ch_tunnel_parse_endpoint(target, out_address, out_port)) return CH_OK;
    char *host = NULL;
    if (!ch_tunnel_split_target(target, &host, out_port)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel target must be host:port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_status status = ch_tunnel_resolve_host(stack, host, out_address, error);
    free(host);
    return status;
}

static int ch_tunnel_parse_prefix(const char *value, ip_addr_t *out_address,
                                  unsigned int *out_prefix) {
    if (value == NULL) return 0;
    const char *slash = strchr(value, '/');
    size_t length = slash == NULL ? strlen(value) : (size_t)(slash - value);
    if (length == 0U || length >= INET6_ADDRSTRLEN) return 0;
    char text[INET6_ADDRSTRLEN];
    memcpy(text, value, length);
    text[length] = '\0';
    if (!ipaddr_aton(text, out_address)) return 0;
    unsigned int maximum = IP_IS_V6(out_address) ? 128U : 32U;
    unsigned int prefix = maximum;
    if (slash != NULL) {
        char *end = NULL;
        errno = 0;
        unsigned long parsed = strtoul(slash + 1U, &end, 10);
        if (errno != 0 || end == slash + 1U || *end != '\0' ||
            parsed > maximum) return 0;
        prefix = (unsigned int)parsed;
    }
    *out_prefix = prefix;
    return 1;
}

static void ch_tunnel_free_dns(ch_tunnel_stack *stack) {
    for (size_t index = 0U; index < stack->dns_server_count; ++index) {
        free(stack->dns_servers[index]);
    }
    free(stack->dns_servers);
    stack->dns_servers = NULL;
    stack->dns_server_count = 0U;
}

ch_tunnel_stack *ch_tunnel_stack_create(
    const ch_tunnel_stack_options *options, ch_error *error) {
    ch_error_clear(error);
    if (options == NULL || options->packet_writer == NULL ||
        options->addresses == NULL || options->address_count == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel addresses and packet writer are required");
        return NULL;
    }
    unsigned int mtu = options->mtu == 0U ? CH_TUNNEL_DEFAULT_MTU :
                                            options->mtu;
    if (mtu < 1280U || mtu > UINT16_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel MTU must be between 1280 and 65535");
        return NULL;
    }
    ch_tunnel_stack *stack = calloc(1U, sizeof(*stack));
    if (stack == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate tunnel stack");
        return NULL;
    }
    stack->wake_read = -1;
    stack->wake_write = -1;
    stack->mtu = mtu;
    stack->packet_writer = options->packet_writer;
    stack->packet_writer_context = options->packet_writer_context;
    for (size_t index = 0U; index < LWIP_IPV6_NUM_ADDRESSES; ++index) {
        stack->ipv6_indices[index] = -1;
    }
    ip4_addr_t ipv4;
    ip4_addr_t netmask;
    ip4_addr_t gateway;
    ip4_addr_set_zero(&ipv4);
    ip4_addr_set_zero(&netmask);
    ip4_addr_set_zero(&gateway);
    for (size_t index = 0U; index < options->address_count; ++index) {
        ip_addr_t address;
        unsigned int prefix = 0U;
        if (!ch_tunnel_parse_prefix(options->addresses[index], &address,
                                    &prefix)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "invalid tunnel address %s",
                         options->addresses[index]);
            free(stack);
            return NULL;
        }
        if (IP_IS_V6(&address)) {
            if (!stack->has_ipv6) stack->local_ipv6 = address;
            stack->has_ipv6 = 1;
        } else if (!stack->has_ipv4) {
            stack->local_ipv4 = address;
            ip4_addr_copy(ipv4, *ip_2_ip4(&address));
            uint32_t mask = prefix == 0U ? 0U :
                UINT32_MAX << (32U - prefix);
            ip4_addr_set_u32(&netmask, lwip_htonl(mask));
            stack->has_ipv4 = 1;
        }
    }
    if (options->dns_server_count > 0U) {
        stack->dns_servers = calloc(options->dns_server_count,
                                    sizeof(*stack->dns_servers));
        if (stack->dns_servers == NULL) {
            free(stack);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate tunnel DNS servers");
            return NULL;
        }
        for (size_t index = 0U; index < options->dns_server_count; ++index) {
            ip_addr_t parsed;
            if (options->dns_servers[index] == NULL ||
                !ipaddr_aton(options->dns_servers[index], &parsed) ||
                (IP_IS_V6(&parsed) && !stack->has_ipv6) ||
                (!IP_IS_V6(&parsed) && !stack->has_ipv4)) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "invalid tunnel DNS server");
                ch_tunnel_free_dns(stack);
                free(stack);
                return NULL;
            }
            stack->dns_servers[index] = ch_strdup(
                options->dns_servers[index]);
            if (stack->dns_servers[index] == NULL) {
                ch_tunnel_free_dns(stack);
                free(stack);
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy tunnel DNS server");
                return NULL;
            }
            ++stack->dns_server_count;
        }
    }
    int wake[2] = {-1, -1};
    if (pipe(wake) != 0 || !ch_tunnel_set_nonblocking(wake[0]) ||
        !ch_tunnel_set_nonblocking(wake[1]) ||
        pthread_mutex_init(&stack->queue_mutex, NULL) != 0) {
        ch_tunnel_close_descriptor(&wake[0]);
        ch_tunnel_close_descriptor(&wake[1]);
        ch_tunnel_free_dns(stack);
        free(stack);
        ch_error_set(error, CH_ERROR_IO,
                     "initialize tunnel worker");
        return NULL;
    }
    stack->wake_read = wake[0];
    stack->wake_write = wake[1];
    ch_lwip_context_lock();
    if (netif_add(&stack->interface, &ipv4, &netmask, &gateway, stack,
                  ch_tunnel_interface_initialize, ip_input) == NULL) {
        ch_lwip_context_unlock();
        ch_tunnel_close_descriptor(&stack->wake_read);
        ch_tunnel_close_descriptor(&stack->wake_write);
        (void)pthread_mutex_destroy(&stack->queue_mutex);
        ch_tunnel_free_dns(stack);
        free(stack);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize tunnel interface");
        return NULL;
    }
    if (stack->has_ipv6) {
        s8_t index = -1;
        if (netif_add_ip6_address(&stack->interface,
                                  ip_2_ip6(&stack->local_ipv6), &index) !=
                ERR_OK || index < 0) {
            netif_remove(&stack->interface);
            ch_lwip_context_unlock();
            ch_tunnel_close_descriptor(&stack->wake_read);
            ch_tunnel_close_descriptor(&stack->wake_write);
            (void)pthread_mutex_destroy(&stack->queue_mutex);
            ch_tunnel_free_dns(stack);
            free(stack);
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "initialize tunnel IPv6 address");
            return NULL;
        }
        stack->ipv6_indices[stack->ipv6_count++] = index;
        netif_ip6_addr_set_state(&stack->interface, index,
                                 IP6_ADDR_PREFERRED);
    }
    netif_set_up(&stack->interface);
    netif_set_link_up(&stack->interface);
    ch_lwip_context_unlock();
    if (pthread_create(&stack->worker, NULL, ch_tunnel_worker_main,
                       stack) != 0) {
        ch_lwip_context_lock();
        netif_set_link_down(&stack->interface);
        netif_set_down(&stack->interface);
        netif_remove(&stack->interface);
        ch_lwip_context_unlock();
        ch_tunnel_close_descriptor(&stack->wake_read);
        ch_tunnel_close_descriptor(&stack->wake_write);
        (void)pthread_mutex_destroy(&stack->queue_mutex);
        ch_tunnel_free_dns(stack);
        free(stack);
        ch_error_set(error, CH_ERROR_IO, "start tunnel worker");
        return NULL;
    }
    return stack;
}

void ch_tunnel_stack_destroy(ch_tunnel_stack *stack) {
    if (stack == NULL) return;
    (void)pthread_mutex_lock(&stack->queue_mutex);
    stack->stopping = 1;
    uint8_t wake = 1U;
    (void)write(stack->wake_write, &wake, sizeof(wake));
    (void)pthread_mutex_unlock(&stack->queue_mutex);
    (void)pthread_join(stack->worker, NULL);
    ch_tunnel_close_descriptor(&stack->wake_read);
    ch_tunnel_close_descriptor(&stack->wake_write);
    (void)pthread_mutex_destroy(&stack->queue_mutex);
    ch_tunnel_free_dns(stack);
    free(stack);
}

ch_status ch_tunnel_stack_inject(ch_tunnel_stack *stack,
                                 const uint8_t *packet, size_t length,
                                 ch_error *error) {
    ch_error_clear(error);
    if (stack == NULL || packet == NULL || length < 20U ||
        length > CH_TUNNEL_MAX_PACKET ||
        (packet[0] >> 4U != 4U && packet[0] >> 4U != 6U)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid tunnel IP packet");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *copy = malloc(length);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy tunnel IP packet");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(copy, packet, length);
    ch_tunnel_command command;
    if (!ch_tunnel_command_initialize(&command, CH_TUNNEL_COMMAND_INJECT)) {
        free(copy);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize tunnel input command");
        return CH_ERROR_INTERNAL;
    }
    command.value.inject.bytes = copy;
    command.value.inject.length = length;
    ch_status status = ch_tunnel_submit(stack, &command, error);
    free(copy);
    return status;
}

ch_status ch_tunnel_stack_dial_tcp(ch_tunnel_stack *stack,
                                   const char *target,
                                   int *out_descriptor,
                                   ch_error *error) {
    ch_error_clear(error);
    if (stack == NULL || target == NULL || out_descriptor == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel stack, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    ip_addr_t address;
    uint16_t port = 0U;
    ch_status status = ch_tunnel_resolve_target(stack, target, &address,
                                                &port, error);
    if (status != CH_OK) return status;
    ch_tunnel_command command;
    if (!ch_tunnel_command_initialize(&command, CH_TUNNEL_COMMAND_DIAL)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize tunnel dial command");
        return CH_ERROR_INTERNAL;
    }
    command.value.dial.address = address;
    command.value.dial.port = port;
    command.value.dial.descriptor = -1;
    status = ch_tunnel_submit(stack, &command, error);
    if (status == CH_OK) *out_descriptor = command.value.dial.descriptor;
    return status;
}

ch_status ch_tunnel_stack_open_packet(ch_tunnel_stack *stack,
                                      ch_tunnel_packet **out_packet,
                                      ch_error *error) {
    ch_error_clear(error);
    if (stack == NULL || out_packet == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel stack and packet output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_packet = NULL;
    ch_tunnel_command command;
    if (!ch_tunnel_command_initialize(&command,
                                      CH_TUNNEL_COMMAND_PACKET_OPEN)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize tunnel packet command");
        return CH_ERROR_INTERNAL;
    }
    ch_status status = ch_tunnel_submit(stack, &command, error);
    if (status == CH_OK) *out_packet = command.value.packet_open.packet;
    return status;
}

ch_status ch_tunnel_packet_send(ch_tunnel_packet *packet,
                                const char *target,
                                const uint8_t *payload,
                                size_t payload_length,
                                ch_error *error) {
    ch_error_clear(error);
    if (packet == NULL || target == NULL ||
        (payload == NULL && payload_length > 0U)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid tunnel UDP send input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    (void)pthread_mutex_lock(&packet->mutex);
    ch_tunnel_stack *stack = packet->stack;
    (void)pthread_mutex_unlock(&packet->mutex);
    if (stack == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "tunnel datagram socket is closed");
        return CH_ERROR_INVALID_STATE;
    }
    ip_addr_t address;
    uint16_t port = 0U;
    ch_status status = ch_tunnel_resolve_target(stack, target, &address,
                                                &port, error);
    if (status != CH_OK) return status;
    return ch_tunnel_packet_send_numeric(packet, &address, port, payload,
                                         payload_length, error);
}

ch_status ch_tunnel_packet_receive(ch_tunnel_packet *packet,
                                   uint8_t *buffer,
                                   size_t buffer_capacity,
                                   size_t *out_length,
                                   char **out_source,
                                   int timeout_milliseconds,
                                   ch_error *error) {
    ch_error_clear(error);
    if (packet == NULL || buffer == NULL || out_length == NULL ||
        out_source == NULL || timeout_milliseconds < -1) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid tunnel UDP receive input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_length = 0U;
    *out_source = NULL;
    (void)pthread_mutex_lock(&packet->mutex);
    int wait_result = 0;
    struct timespec deadline = {0};
    if (timeout_milliseconds > 0) {
        (void)clock_gettime(CLOCK_REALTIME, &deadline);
        deadline.tv_sec += timeout_milliseconds / 1000;
        deadline.tv_nsec += (long)(timeout_milliseconds % 1000) * 1000000L;
        if (deadline.tv_nsec >= 1000000000L) {
            ++deadline.tv_sec;
            deadline.tv_nsec -= 1000000000L;
        }
    }
    while (packet->head == NULL && !packet->closed) {
        if (timeout_milliseconds == 0) break;
        wait_result = timeout_milliseconds < 0 ?
            pthread_cond_wait(&packet->condition, &packet->mutex) :
            pthread_cond_timedwait(&packet->condition, &packet->mutex,
                                   &deadline);
        if (wait_result == ETIMEDOUT) break;
    }
    ch_tunnel_datagram *item = packet->head;
    if (item != NULL) {
        packet->head = item->next;
        if (packet->head == NULL) packet->tail = NULL;
        --packet->queued;
    }
    int closed = packet->closed;
    (void)pthread_mutex_unlock(&packet->mutex);
    if (item == NULL) {
        ch_error_set(error, closed ? CH_ERROR_INVALID_STATE :
                                     CH_ERROR_NOT_FOUND,
                     closed ? "tunnel datagram socket is closed" :
                              "tunnel UDP receive timed out");
        return closed ? CH_ERROR_INVALID_STATE : CH_ERROR_NOT_FOUND;
    }
    if (item->length > buffer_capacity) {
        free(item->bytes);
        free(item->source);
        free(item);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "tunnel UDP receive buffer is too small");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memcpy(buffer, item->bytes, item->length);
    *out_length = item->length;
    *out_source = item->source;
    free(item->bytes);
    free(item);
    return CH_OK;
}

void ch_tunnel_packet_close(ch_tunnel_packet *packet) {
    if (packet == NULL) return;
    (void)pthread_mutex_lock(&packet->mutex);
    ch_tunnel_stack *stack = packet->stack;
    int closed = packet->closed;
    (void)pthread_mutex_unlock(&packet->mutex);
    if (stack != NULL && !closed) {
        ch_tunnel_command command;
        if (ch_tunnel_command_initialize(&command,
                                         CH_TUNNEL_COMMAND_PACKET_CLOSE)) {
            command.value.packet_close.packet = packet;
            ch_error ignored;
            (void)ch_tunnel_submit(stack, &command, &ignored);
        }
    }
    (void)pthread_mutex_lock(&packet->mutex);
    ch_tunnel_packet_queue_clear(packet);
    (void)pthread_mutex_unlock(&packet->mutex);
    (void)pthread_cond_destroy(&packet->condition);
    (void)pthread_mutex_destroy(&packet->mutex);
    free(packet);
}
