#ifndef CLAMBHOOK_LWIPOPTS_H
#define CLAMBHOOK_LWIPOPTS_H

/* Single-threaded raw API: ch_lwip_context serializes every interface. */
#define NO_SYS 1
#define SYS_LIGHTWEIGHT_PROT 0
#define LWIP_NETCONN 0
#define LWIP_SOCKET 0
#define LWIP_COMPAT_SOCKETS 0
#define LWIP_DONT_PROVIDE_BYTEORDER_FUNCTIONS 1

#define LWIP_IPV4 1
#define LWIP_IPV6 1
#define LWIP_IPV6_SCOPES 0
#define LWIP_IPV6_SCOPES_DEBUG 0
#define LWIP_IPV6_AUTOCONFIG 0
#define LWIP_IPV6_DHCP6 0
#define LWIP_IPV6_MLD 0
#define LWIP_ND6_QUEUEING 0

#define LWIP_TCP 1
#define LWIP_UDP 1
#define LWIP_RAW 0
#define LWIP_ICMP 1
#define LWIP_ICMP6 1
#define LWIP_IGMP 0
#define LWIP_DNS 0

/* The VPN boundary carries complete layer-3 packets, never Ethernet frames. */
#define LWIP_ETHERNET 0
#define LWIP_ARP 0
#define LWIP_DHCP 0
#define LWIP_AUTOIP 0
#define LWIP_ACD 0
#define IP_FORWARD 0
#define IP_OPTIONS_ALLOWED 0
#define LWIP_SINGLE_NETIF 0
#define LWIP_NETIF_LOOPBACK 0

#define IP_REASSEMBLY 1
#define IP_FRAG 1
#define LWIP_IPV6_REASS 1
#define LWIP_IPV6_FRAG 1
/* Required when sanitizers enlarge lwIP's private reassembly bookkeeping. */
#define IPV6_FRAG_COPYHEADER 1

#define LWIP_TIMERS 1
#define LWIP_TCP_KEEPALIVE 1
#define LWIP_TCP_TIMESTAMPS 1
#define TCP_QUEUE_OOSEQ 1
#define LWIP_WND_SCALE 1
#define TCP_RCV_SCALE 3
#define TCP_MSS 1360
#define TCP_WND (64 * 1024)
#define TCP_SND_BUF (64 * 1024)
#define TCP_SND_QUEUELEN ((4 * TCP_SND_BUF + TCP_MSS - 1) / TCP_MSS)

#define MEM_LIBC_MALLOC 1
#define MEMP_MEM_MALLOC 1
#define MEM_SIZE (4 * 1024 * 1024)
#define MEMP_NUM_TCP_PCB 512
#define MEMP_NUM_TCP_PCB_LISTEN 512
#define MEMP_NUM_UDP_PCB 512
#define MEMP_NUM_TCP_SEG 2048
#define PBUF_POOL_SIZE 1024
#define PBUF_POOL_BUFSIZE 1536

#define LWIP_STATS 0
#define LWIP_DEBUG 0

#endif
