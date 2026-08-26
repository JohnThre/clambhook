#include "clambhook/ip_stack.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/random.h>
#include <time.h>
#include <unistd.h>

#include "lwip/init.h"
#include "lwip/ip.h"
#include "lwip/ip4_addr.h"
#include "lwip/ip6_addr.h"
#include "lwip/netif.h"
#include "lwip/pbuf.h"
#include "lwip/timeouts.h"

#include "internal.h"

#define CH_IP_STACK_DEFAULT_MTU 1500U
#define CH_IP_STACK_MAX_PACKET 65535U

struct ch_ip_stack {
    struct netif interface;
    ch_ip_stack_packet_writer packet_writer;
    void *packet_writer_context;
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
    struct pbuf *buffer = pbuf_alloc(PBUF_RAW, (u16_t)length, PBUF_RAM);
    if (buffer == NULL || pbuf_take(buffer, packet, (u16_t)length) != ERR_OK) {
        if (buffer != NULL) (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate IP packet buffer");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    err_t result = stack->interface.input(buffer, &stack->interface);
    if (result != ERR_OK) {
        (void)pbuf_free(buffer);
        ch_error_set(error, CH_ERROR_PARSE,
                     "IP stack rejected packet with error %d", (int)result);
        return CH_ERROR_PARSE;
    }
    sys_check_timeouts();
    return CH_OK;
}

void ch_ip_stack_tick(ch_ip_stack *stack) {
    if (stack != NULL) sys_check_timeouts();
}
